// mysqlmem invokes the Bao-linked MySQL server in this Go process.
package main

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"mysqlmem/internal/parentwatch"
	"mysqlmem/native"
)

func main() {
	if err := parentwatch.WatchFromEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "mysqlmem: parent watch:", err)
		os.Exit(2)
	}
	runtime.LockOSThread()
	// MySQL retains argv for the lifetime of mysqld_main. Pin both the strings
	// and argv until that call returns; include the conventional null sentinel.
	var pins runtime.Pinner
	defer pins.Unpin()
	storage := make([][]byte, len(os.Args))
	argv := make([]*byte, len(os.Args)+1)
	for i, arg := range os.Args {
		storage[i] = append([]byte(arg), 0)
		pins.Pin(&storage[i][0])
		argv[i] = &storage[i][0]
	}
	pins.Pin(&argv[0])
	status := native.ServerMain(int32(len(os.Args)), unsafe.Pointer(&argv[0]))
	runtime.KeepAlive(storage)
	os.Exit(int(status))
}
