//go:build darwin

package discovery

/*
#include <stdint.h>
#include <stdlib.h>
#include <dns_sd.h>

void *kopi_queue_new(void);
void kopi_queue_release(void *q);
int32_t kopi_register(void *q, const char *name, const char *type, uint16_t port,
	const void *txt, uint16_t txtLen, DNSServiceRef *out);
int32_t kopi_browse(void *q, const char *type, uintptr_t ctx, DNSServiceRef *out);
int32_t kopi_resolve(void *q, uint32_t ifIndex, const char *name, const char *type,
	const char *domain, uintptr_t ctx, DNSServiceRef *out);
int32_t kopi_addrinfo(void *q, uint32_t ifIndex, const char *host, uintptr_t ctx,
	DNSServiceRef *out);
void kopi_dealloc_sync(void *q, DNSServiceRef *refs, int n);
void kopi_dealloc_now(DNSServiceRef ref);
*/
import "C"

import (
	"context"
	"fmt"
	"net/netip"
	"runtime/cgo"
	gosync "sync"
	"unsafe"

	"github.com/umidscodes/kopi/internal/transport"
)

const kDNSServiceFlagsAdd = 0x2

// MDNS announces this device and browses for others through the system Bonjour
// service (mDNSResponder). On macOS that daemon owns port 5353; asking it is the
// supported way, and it is what Local Network privacy expects.
// Only device ID, name and protocol version are published; nothing secret.
type MDNS struct {
	Self transport.Peer // ID and Name; Addrs are ignored
	Port int
}

// darwinRun is the state of one Run. Callbacks reach it through cgo handles.
type darwinRun struct {
	b     *browser
	queue unsafe.Pointer

	mu        gosync.Mutex
	closed    bool
	instances map[string]*instance // key: name + interface index
	handles   []cgo.Handle
	refs      []C.DNSServiceRef // browse + register, freed at the end
}

// instance is one discovered service on one interface.
type instance struct {
	run     *darwinRun
	key     string
	ifIndex uint32
	resolve C.DNSServiceRef
	addr    C.DNSServiceRef
	peer    transport.Peer // filled by resolve
	port    uint16
}

func (m MDNS) Run(ctx context.Context, emit func(Event)) error {
	r := &darwinRun{
		b:         newBrowser(m.Self.ID, emit),
		queue:     C.kopi_queue_new(),
		instances: map[string]*instance{},
	}
	defer C.kopi_queue_release(r.queue)

	ctype := C.CString(ServiceType)
	defer C.free(unsafe.Pointer(ctype))

	// Register. The TXT record is copied by DNSServiceRegister.
	name := C.CString(instanceName(m.Self))
	txt := encodeTXT(txtRecord(m.Self))
	ctxt := C.CBytes(txt)
	var reg C.DNSServiceRef
	err := C.kopi_register(r.queue, name, ctype, C.uint16_t(m.Port), ctxt, C.uint16_t(len(txt)), &reg)
	C.free(unsafe.Pointer(name))
	C.free(ctxt)
	if err != 0 {
		return fmt.Errorf("bonjour register: error %d", int32(err))
	}
	r.refs = append(r.refs, reg)

	// Browse.
	h := r.handle(r)
	var br C.DNSServiceRef
	if err := C.kopi_browse(r.queue, ctype, C.uintptr_t(h), &br); err != 0 {
		r.shutdown()
		return fmt.Errorf("bonjour browse: error %d", int32(err))
	}
	r.mu.Lock()
	r.refs = append(r.refs, br)
	r.mu.Unlock()

	<-ctx.Done()
	r.shutdown() // deallocating the registration sends the goodbye (DISC-2)
	return nil
}

func (r *darwinRun) handle(v any) cgo.Handle {
	h := cgo.NewHandle(v)
	r.mu.Lock()
	r.handles = append(r.handles, h)
	r.mu.Unlock()
	return h
}

func (r *darwinRun) shutdown() {
	r.mu.Lock()
	r.closed = true
	refs := append([]C.DNSServiceRef(nil), r.refs...)
	for _, in := range r.instances {
		refs = append(refs, in.resolve, in.addr)
	}
	r.instances = map[string]*instance{}
	handles := r.handles
	r.handles = nil
	r.mu.Unlock()

	if len(refs) > 0 {
		arr := (*C.DNSServiceRef)(C.malloc(C.size_t(len(refs)) * C.size_t(unsafe.Sizeof(refs[0]))))
		copy(unsafe.Slice(arr, len(refs)), refs)
		C.kopi_dealloc_sync(r.queue, arr, C.int(len(refs)))
		C.free(unsafe.Pointer(arr))
	}
	// After the synchronous teardown no callback can still be running or queued.
	for _, h := range handles {
		h.Delete()
	}
}

//export goBrowse
func goBrowse(ctx C.uintptr_t, flags C.uint32_t, ifIndex C.uint32_t, errCode C.int32_t, name, typ, domain *C.char) {
	if errCode != 0 {
		return
	}
	r := cgo.Handle(ctx).Value().(*darwinRun)
	key := fmt.Sprintf("%s%%%d", C.GoString(name), uint32(ifIndex))

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	if flags&kDNSServiceFlagsAdd == 0 {
		// Removed (goodbye or expiry) on this interface.
		in := r.instances[key]
		delete(r.instances, key)
		var id string
		stillThere := false
		if in != nil {
			id = in.peer.ID
			for _, other := range r.instances {
				if other.peer.ID == id {
					stillThere = true
				}
			}
			C.kopi_dealloc_now(in.resolve)
			C.kopi_dealloc_now(in.addr)
		}
		r.mu.Unlock()
		if id != "" && !stillThere {
			r.b.gone(id)
		}
		return
	}
	if _, ok := r.instances[key]; ok {
		r.mu.Unlock()
		return
	}
	in := &instance{run: r, key: key, ifIndex: uint32(ifIndex)}
	r.instances[key] = in
	r.mu.Unlock()

	h := r.handle(in)
	var ref C.DNSServiceRef
	if C.kopi_resolve(r.queue, ifIndex, name, typ, domain, C.uintptr_t(h), &ref) == 0 {
		r.mu.Lock()
		in.resolve = ref
		r.mu.Unlock()
	}
}

//export goResolve
func goResolve(ctx C.uintptr_t, ifIndex C.uint32_t, errCode C.int32_t, host *C.char, port C.uint16_t, txtLen C.uint16_t, txt unsafe.Pointer) {
	in := cgo.Handle(ctx).Value().(*instance)
	r := in.run
	if errCode != 0 {
		return
	}
	kv := decodeTXT(C.GoBytes(txt, C.int(txtLen)))
	r.mu.Lock()
	if r.closed || r.instances[in.key] != in || kv["id"] == "" {
		r.mu.Unlock()
		return
	}
	in.peer = transport.Peer{ID: kv["id"], Name: kv["name"], Version: versionFromTXT(kv)}
	in.port = uint16(port)
	// One answer is enough; stop resolving and look up the host's addresses.
	C.kopi_dealloc_now(in.resolve)
	in.resolve = nil
	if in.addr != nil {
		C.kopi_dealloc_now(in.addr)
		in.addr = nil
	}
	r.mu.Unlock()

	var ref C.DNSServiceRef
	if C.kopi_addrinfo(r.queue, ifIndex, host, ctx, &ref) == 0 {
		r.mu.Lock()
		in.addr = ref
		r.mu.Unlock()
	}
}

//export goAddr
func goAddr(ctx C.uintptr_t, flags C.uint32_t, errCode C.int32_t, ip *C.uchar, n C.int) {
	in := cgo.Handle(ctx).Value().(*instance)
	r := in.run
	if errCode != 0 || flags&kDNSServiceFlagsAdd == 0 || n == 0 {
		return
	}
	a, ok := usable(C.GoBytes(unsafe.Pointer(ip), n))
	if !ok {
		return
	}
	r.mu.Lock()
	if r.closed || r.instances[in.key] != in {
		r.mu.Unlock()
		return
	}
	p := in.peer
	p.Addrs = []netip.AddrPort{netip.AddrPortFrom(a, in.port)}
	r.mu.Unlock()
	r.b.seen(p)
}

// encodeTXT builds DNS TXT rdata: each entry is a length byte then "key=value".
func encodeTXT(kv map[string]string) []byte {
	var out []byte
	for _, k := range []string{"id", "name", "v"} { // fixed order: deterministic
		e := k + "=" + kv[k]
		if len(e) > 255 {
			e = e[:255]
		}
		out = append(out, byte(len(e)))
		out = append(out, e...)
	}
	return out
}

func decodeTXT(b []byte) map[string]string {
	kv := map[string]string{}
	for len(b) > 0 {
		n := int(b[0])
		b = b[1:]
		if n > len(b) {
			break
		}
		e := string(b[:n])
		b = b[n:]
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				kv[e[:i]] = e[i+1:]
				break
			}
		}
	}
	return kv
}
