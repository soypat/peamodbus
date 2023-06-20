package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/soypat/peamodbus"
	"github.com/soypat/peamodbus/modbustcp"
)

// This program creates a modbus server that adds 1 to
// all holding registers on every client request.

func main() {
	dataBank := peamodbus.ConcurrencySafeDataModel(&peamodbus.BlockedModel{})
	sv, err := modbustcp.NewServer(modbustcp.ServerConfig{
		Address:        "localhost:8080",
		ConnectTimeout: 5 * time.Second,
		DataModel:      dataBank,
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	for {
		if !sv.IsConnected() {
			fmt.Println("attempting connection")
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
			time.Sleep(time.Second)
		} else {
			for addr := 0; addr < 125; addr++ {
				value := dataBank.GetHoldingRegister(addr)
				dataBank.SetHoldingRegister(addr, value+1)
			}
		}
		if err := sv.Err(); err != nil {
			log.Println("server error:", err)
		}
	}
}
