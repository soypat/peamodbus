package rtu

import "github.com/soypat/peamodbus"

type PDUHeader struct {
	Address      uint8
	FunctionCode peamodbus.FunctionCode
}

type Server struct {
}
