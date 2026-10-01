package main

import (
	"log"
	"os"

	"golang.org/x/sys/windows/svc"
)

// serviceName is the name scripts/windows-service.sh registers.
const serviceName = "Bibli"

// runAsService hooks the Windows service manager when it started Bibli: a stop
// or shutdown request becomes the same interrupt as Ctrl-C, so the server
// closes cleanly. The returned function reports the service stopped; call it
// last, once the database is closed.
func runAsService(stop chan<- os.Signal) func() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("service: %v", err)
	}
	if !isService {
		return func() {}
	}
	h := &service{stop: stop, done: make(chan struct{})}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		if err := svc.Run(serviceName, h); err != nil {
			log.Fatalf("service: %v", err)
		}
	}()
	return func() { close(h.done); <-exited }
}

type service struct {
	stop chan<- os.Signal
	done chan struct{}
}

func (h *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				// Above the 10 s main gives in-flight requests.
				status <- svc.Status{State: svc.StopPending, WaitHint: 20000}
				select {
				case h.stop <- os.Interrupt:
				default: // a stop is already on its way
				}
			}
		case <-h.done:
			return false, 0
		}
	}
}
