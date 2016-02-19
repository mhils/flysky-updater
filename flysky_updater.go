package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/cheggaaa/pb"
	"github.com/tarm/serial"
	"gopkg.in/alecthomas/kingpin.v2"
	"io/ioutil"
	"time"
)

var (
	port     = kingpin.Arg("port", "port").Required().String()
	filename = kingpin.Arg("filename", "filename").Required().String()
	force    = kingpin.Flag("force", "Force flash.").Short('f').Bool()
	verbose  = kingpin.Flag("verbose", "Verbose mode.").Short('v').Bool()
)

func make_checksum(payload []byte) []byte {
	var checksum uint16 = 0xFFFF
	for i := 0; i < len(payload); i++ {
		checksum -= uint16(payload[i])
	}
	ret := make([]byte, 2)
	binary.LittleEndian.PutUint16(ret, checksum)
	return ret
}

func WriteAll(s *serial.Port, raw []byte) error {
	if *verbose {
		fmt.Println("Write", raw[:2], raw[2:len(raw)-2], raw[len(raw)-2:])
	}
	n, err := s.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return errors.New("Didn't write all bytes.")
	}
	return nil
}

func WriteFrame(s *serial.Port, payload []byte) error {
	length := make([]byte, 2)
	binary.LittleEndian.PutUint16(length, uint16(len(payload)+4))
	frame := append(length, payload...)
	frame = append(frame, make_checksum(frame)...)
	return WriteAll(s, frame)
}

func ReadAll(s *serial.Port, n int) ([]byte, error) {
	buf := make([]byte, n)
	bytes_read := 0
	for bytes_read < n {
		c, err := s.Read(buf[bytes_read:])
		if err != nil {
			return nil, err
		}
		if c == 0 {
			return nil, errors.New("Read timeout")
		}
		bytes_read += c
	}
	return buf, nil
}

func EmptyRx(s *serial.Port) {
	c := 1
	buf := make([]byte, 1024)
	for c > 0 {
		c, _ = s.Read(buf)
	}
}

func ReadFrame(s *serial.Port) ([]byte, error) {
	head, err := ReadAll(s, 3)
	if err != nil {
		return nil, err
	}
	if head[0] != 0x55 {
		return nil, errors.New("Invalid response")
	}
	size := int(binary.LittleEndian.Uint16(head[1:]))
	body, err := ReadAll(s, size-3)
	if err != nil {
		return nil, err
	}
	payload := body[:len(body)-2]
	checksum := body[len(body)-2:]
	if *verbose {
		fmt.Println("Read", head, payload, checksum)
	}
	checksum_cmp := make_checksum(append(head, payload...))
	if !bytes.Equal(checksum, checksum_cmp) {
		return nil, errors.New("Invalid checksum")
	}
	return payload, nil
}

func ping(s *serial.Port) ([]byte, error) {
	err := WriteFrame(s, []byte{0xC0})
	if err != nil {
		return nil, err
	}
	answer, err := ReadFrame(s)
	if err != nil {
		return nil, err
	}
	if answer[0] != 0xC0 {
		return nil, errors.New("Unexpected answer to ping")
	}
	return answer, nil
}

func communicate(s *serial.Port, request []byte, response []byte) error {
	err := WriteFrame(s, request)
	if err != nil {
		return err
	}
	msg, err := ReadFrame(s)
	if err != nil {
		return err
	}
	if !bytes.Equal(msg, response) {
		errors.New("Unexpected response: " + hex.Dump(response))
	}
	return nil
}

func ask_write(s *serial.Port, address int) error {
	ask_permission := []byte{0xc2, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	binary.LittleEndian.PutUint16(ask_permission[1:3], uint16(address))
	get_permission := []byte{0xc2, 0x80, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	binary.LittleEndian.PutUint16(get_permission[2:4], uint16(address))

	return communicate(s, ask_permission, get_permission)
}

func write_chunk(s *serial.Port, address int, data []byte) error {
	write_instruction := []byte{0xc3, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}
	binary.LittleEndian.PutUint16(write_instruction[1:3], uint16(address))
	write_instruction = append(write_instruction, data...)
	write_confirmation := []byte{0xc3, 0x00, 0x00, 0x00, 0x00}

	return communicate(s, write_instruction, write_confirmation)
}

func update(s *serial.Port, firmware []byte) error {
	start_address := 0x1800

	bar := pb.New(len(firmware)).SetUnits(pb.U_BYTES)
	bar.Start()

	for bytes_written := 0; bytes_written < len(firmware); bytes_written += 1024 {
		tries := 0
	ask:
		err := ask_write(s, start_address+bytes_written)
		if err != nil {
			tries++
			if tries <= 3 {
				EmptyRx(s)
				goto ask
			}
		}
		for chunk := 0; chunk < 1024; chunk += 256 {
			err = write_chunk(s, start_address+bytes_written+chunk, firmware[bytes_written+chunk:bytes_written+chunk+256])
			if err != nil {
				tries++
				if tries <= 3 {
					EmptyRx(s)
					goto ask
				}
			}
			bar.Add(256)
		}
	}

	bar.FinishPrint("Upload completed.")
	return nil
}

func restart(s *serial.Port) error {
	return WriteFrame(s, []byte{0xC1, 0x00})
}

func main() {
	kingpin.Parse()

	data, err := ioutil.ReadFile(*filename)
	if err != nil {
		panic(err)
	}

	if (len(data) < 0x9000 || len(data) > 0xe7ff) && !*force {
		panic(fmt.Sprintf("Unexpected firmare size: %d bytes", len(data)))
	}

	c := &serial.Config{Name: *port, Baud: 115200, ReadTimeout: time.Second * 1}
	s, err := serial.OpenPort(c)
	if err != nil {
		panic(err)
	}

	_, err = ping(s)
	if err != nil {
		panic(err)
	}

	err = update(s, data)
	if err != nil {
		panic(err)
	}

	err = restart(s)
	if err != nil {
		panic(err)
	}

	fmt.Println("Success!")

	s.Close()
}
