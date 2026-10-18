// Package clip defines the clipboard abstraction the sync engine works against.
// OS implementations live in build-tagged files; Fake is for tests.
package clip

import "context"

// Item is one local clipboard change.
type Item struct {
	Text string
	// Sensitive is true when the source app marked the content as private
	// (password managers do this). See SPEC.md, Feature: Sensitive content.
	Sensitive bool
}

// Clipboard is the system clipboard.
type Clipboard interface {
	// Watch emits an Item for every clipboard change until ctx is done.
	// Changes caused by Write are emitted too, exactly as the OS reports them;
	// filtering those out is the sync engine's job.
	Watch(ctx context.Context) (<-chan Item, error)
	// Write replaces the clipboard contents with plain text.
	Write(text string) error
}
