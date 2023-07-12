package main

import (
	"os"
	"time"

	"github.com/soypat/peamodbus/modbusrtu"
	"go.bug.st/serial"
	"golang.org/x/exp/slog"
)

func main() {
	const (
		deviceAddress         = 86
		sensorRegisterAddress = 0
	)
	port, err := serial.Open("/dev/ttyUSB0", &serial.Mode{
		BaudRate: 9600,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		panic(err)
	}
	defer port.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	c := modbusrtu.NewClient(modbusrtu.ClientConfig{
		// RxTimeout: time.Second,
		Logger: logger,
	})

	c.SetTransport(port)
	for {
		var tempHumidity [2]uint16
		err = c.ReadHoldingRegisters(deviceAddress, sensorRegisterAddress, tempHumidity[:])
		if err != nil {
			panic(err)
		}
		logger.Info("read", "humidity", float32(tempHumidity[0])/10, "temperature", float32(tempHumidity[1])/10)
		time.Sleep(time.Second)
	}
}
