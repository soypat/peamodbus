package main

import (
	"context"
	"log"
	"time"

	"github.com/soypat/peamodbus"
)

// This program creates a modbus server that adds 1 to
// all holding registers on every client request.

func main() {
	var dataBank peamodbus.BlockedModel
	sv, err := peamodbus.NewServer(peamodbus.ServerConfig{
		Address:        "localhost:8080",
		ConnectTimeout: 5 * time.Second,
		DataModel:      &dataBank,
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	for {
		if !sv.IsConnected() {
			err = sv.Accept(ctx)
			if err != nil {
				log.Println("error connecting", err)
			}
			time.Sleep(time.Second)
			continue
		}
		err = sv.HandleNext()
		if err != nil {
			log.Println("error in HandleNext", err)
		} else {
			for i := range dataBank.HoldingRegisters {
				dataBank.HoldingRegisters[i]++
			}
		}
		if err := sv.Err(); err != nil {
			log.Println("server error:", err)
		}
	}
}
