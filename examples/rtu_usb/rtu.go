package main

import (
	"io"
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

	logfp, _ := os.Create("log.txt")
	defer logfp.Close()

	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stdout, logfp), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	c := modbusrtu.NewClient(modbusrtu.ClientConfig{
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
