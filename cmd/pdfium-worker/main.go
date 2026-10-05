// Command pdfium-worker runs PDFium as a separate process.
//
// The server never links PDFium directly: it spawns workers like this one and
// calls them over go-plugin. Keeping the C++ library out of the server process
// means a PDFium crash takes down only the worker, and the pool discards it and
// starts a replacement.
//
// This binary is not meant to be run by hand; the pool needs it on disk.
package main

import "github.com/klippa-app/go-pdfium/multi_threaded/worker"

func main() {
	worker.StartWorker(nil)
}
