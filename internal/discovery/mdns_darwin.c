// Native Bonjour (dns_sd.h) glue for macOS. All callbacks run on one serial dispatch
// queue and forward to exported Go functions (see mdns_darwin.go).

#include <dns_sd.h>
#include <dispatch/dispatch.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <string.h>
#include "_cgo_export.h"

void *kopi_queue_new(void) {
	return (void *)dispatch_queue_create("kopi.discovery", DISPATCH_QUEUE_SERIAL);
}

void kopi_queue_release(void *q) {
	dispatch_release((dispatch_queue_t)q);
}

// --- register ---

int32_t kopi_register(void *q, const char *name, const char *type, uint16_t port,
		const void *txt, uint16_t txtLen, DNSServiceRef *out) {
	DNSServiceErrorType err = DNSServiceRegister(out, 0, kDNSServiceInterfaceIndexAny,
		name, type, NULL, NULL, htons(port), txtLen, txt, NULL, NULL);
	if (err != kDNSServiceErr_NoError) {
		return err;
	}
	return DNSServiceSetDispatchQueue(*out, (dispatch_queue_t)q);
}

// --- browse ---

static void browse_cb(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifIndex,
		DNSServiceErrorType err, const char *name, const char *type, const char *domain,
		void *ctx) {
	goBrowse((uintptr_t)ctx, flags, ifIndex, err, (char *)name, (char *)type, (char *)domain);
}

int32_t kopi_browse(void *q, const char *type, uintptr_t ctx, DNSServiceRef *out) {
	DNSServiceErrorType err = DNSServiceBrowse(out, 0, kDNSServiceInterfaceIndexAny,
		type, NULL, browse_cb, (void *)ctx);
	if (err != kDNSServiceErr_NoError) {
		return err;
	}
	return DNSServiceSetDispatchQueue(*out, (dispatch_queue_t)q);
}

// --- resolve: instance -> host, port, TXT ---

static void resolve_cb(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifIndex,
		DNSServiceErrorType err, const char *fullname, const char *host, uint16_t port,
		uint16_t txtLen, const unsigned char *txt, void *ctx) {
	goResolve((uintptr_t)ctx, ifIndex, err, (char *)host, ntohs(port), txtLen, (void *)txt);
}

int32_t kopi_resolve(void *q, uint32_t ifIndex, const char *name, const char *type,
		const char *domain, uintptr_t ctx, DNSServiceRef *out) {
	DNSServiceErrorType err = DNSServiceResolve(out, 0, ifIndex, name, type, domain,
		resolve_cb, (void *)ctx);
	if (err != kDNSServiceErr_NoError) {
		return err;
	}
	return DNSServiceSetDispatchQueue(*out, (dispatch_queue_t)q);
}

// --- address lookup: host -> IPs ---

static void addr_cb(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifIndex,
		DNSServiceErrorType err, const char *host, const struct sockaddr *addr,
		uint32_t ttl, void *ctx) {
	unsigned char ip[16];
	int n = 0;
	if (err == kDNSServiceErr_NoError && addr != NULL) {
		if (addr->sa_family == AF_INET) {
			memcpy(ip, &((const struct sockaddr_in *)addr)->sin_addr, 4);
			n = 4;
		} else if (addr->sa_family == AF_INET6) {
			memcpy(ip, &((const struct sockaddr_in6 *)addr)->sin6_addr, 16);
			n = 16;
		}
	}
	goAddr((uintptr_t)ctx, flags, err, ip, n);
}

int32_t kopi_addrinfo(void *q, uint32_t ifIndex, const char *host, uintptr_t ctx,
		DNSServiceRef *out) {
	DNSServiceErrorType err = DNSServiceGetAddrInfo(out, 0, ifIndex,
		kDNSServiceProtocol_IPv4 | kDNSServiceProtocol_IPv6, host, addr_cb, (void *)ctx);
	if (err != kDNSServiceErr_NoError) {
		return err;
	}
	return DNSServiceSetDispatchQueue(*out, (dispatch_queue_t)q);
}

// --- teardown, always on the queue so no callback races the deallocation ---

typedef struct {
	DNSServiceRef *refs;
	int n;
} dealloc_job;

static void dealloc_fn(void *p) {
	dealloc_job *job = (dealloc_job *)p;
	for (int i = 0; i < job->n; i++) {
		if (job->refs[i] != NULL) {
			DNSServiceRefDeallocate(job->refs[i]);
		}
	}
}

// From outside the queue (Go's Run shutting down).
void kopi_dealloc_sync(void *q, DNSServiceRef *refs, int n) {
	dealloc_job job = {refs, n};
	dispatch_sync_f((dispatch_queue_t)q, &job, dealloc_fn);
}

// From inside a callback (already on the queue).
void kopi_dealloc_now(DNSServiceRef ref) {
	if (ref != NULL) {
		DNSServiceRefDeallocate(ref);
	}
}
