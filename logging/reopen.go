package logging

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// SetupLogReopen installs a SIGUSR1 handler that reopens the log file.
func SetupLogReopen(logger *AccessLogger) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			log.Println("SIGUSR1: reopening log file")
			if err := logger.Reopen(); err != nil {
				log.Printf("failed to reopen log: %v", err)
			}
		}
	}()
}
