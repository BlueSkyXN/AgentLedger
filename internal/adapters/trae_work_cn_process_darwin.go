//go:build darwin && cgo

package adapters

/*
#include <libproc.h>
#include <sys/proc_info.h>
*/
import "C"

import (
	"fmt"
	"sort"
	"syscall"
	"unsafe"
)

func traeWorkCNPlatformSupported() bool { return true }

func findTraeWorkCNProcesses(paths []string) ([]int, error) {
	pids := make([]C.int, 4096)
	bytes := C.proc_listpids(C.PROC_ALL_PIDS, 0, unsafe.Pointer(&pids[0]), C.int(len(pids))*C.int(unsafe.Sizeof(pids[0])))
	if bytes <= 0 {
		return nil, fmt.Errorf("process enumeration failed")
	}
	count := int(bytes) / int(unsafe.Sizeof(pids[0]))
	processes := make([]int, 0, 1)
	for _, rawPID := range pids[:count] {
		if rawPID <= 0 {
			continue
		}
		buffer := make([]C.char, int(C.PROC_PIDPATHINFO_MAXSIZE))
		if C.proc_pidpath(rawPID, unsafe.Pointer(&buffer[0]), C.uint32_t(len(buffer))) <= 0 {
			continue
		}
		if matchesTraeWorkCNExecutable(C.GoString(&buffer[0]), paths) {
			processes = append(processes, int(rawPID))
		}
	}
	sort.Ints(processes)
	return processes, nil
}

func signalTraeWorkCNInspector(pid int) error {
	return syscall.Kill(pid, syscall.SIGUSR1)
}
