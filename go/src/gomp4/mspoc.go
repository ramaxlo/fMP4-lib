package main

// #include <stdio.h>
import "C"

import (
	"errors"
	"fmt"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"unsafe"
)

var camera_ch = make(chan *websocket.Conn)
var client_ch = make(chan *websocket.Conn)
var err_ch = make(chan error, 1)
var frame_ch chan []byte

//export GoMP4Callback
func GoMP4Callback(buf *C.uchar, size C.int) C.int {
	gobuf := C.GoBytes(unsafe.Pointer(buf), size)
	fmt.Printf("Got buffer %d\n", len(gobuf))

	frame_ch <- gobuf

	return size
}

func write_frame(writer *websocket.Conn) {
	for {
		select {
		case s := <-frame_ch:
			fmt.Printf("Write buf: %d\n", len(s))

			if len(s) == 0 {
				return
			}

			// Note: We must use websocket.Message to send binary frames
			// The websocket.Conn.Write can't achieve that
			msg := websocket.Message
			err := msg.Send(writer, s)
			if err != nil {
				fmt.Println(err)
			}
		}
	}
}

func conv2int(buf []byte) int {
	var tmp int = 0

	tmp |= int(buf[0])
	tmp |= int(buf[1]) << 8
	tmp |= int(buf[2]) << 16
	tmp |= int(buf[3]) << 24

	return tmp
}

func read_buffer(reader io.ReadCloser) (bool, int, []byte, error) {
	hdr := make([]byte, 9)

	n, err := reader.Read(hdr)
	if err != nil {
		fmt.Println(err)
		return false, 0, nil, err
	}

	if n != 9 {
		fmt.Println("The hdr size unmatched")
		return false, 0, nil, errors.New("Invalid hdr size")
	}

	is_key_frame := false
	if hdr[0] == 1 {
		is_key_frame = true
	}

	duration := conv2int(hdr[1:5])
	size := conv2int(hdr[5:9])
	buf := make([]byte, 0)

	total := size
	for total > 0 {
		tmp := make([]byte, total)

		n, err = reader.Read(tmp)
		if err != nil {
			fmt.Println(err)
			return false, 0, nil, err
		}

		buf = append(buf, tmp[:n]...)

		total -= n
	}

	return is_key_frame, duration, buf[:size], nil
}

func process(writer *websocket.Conn, reader *websocket.Conn) error {
	var mp4writer = NewMP4()
	defer mp4writer.Release()

	frame_ch = make(chan []byte, 1)

	go write_frame(writer)

	for {
		is_key_frame, duration, buf, err := read_buffer(reader)
		if err != nil {
			frame_ch <- make([]byte, 0)
			return err
		}

		size := len(buf)
		fmt.Printf("frame: %v, %d, %d\n", is_key_frame, duration, size)

		// Do some prcocess
		err = mp4writer.WriteH264Sample(buf, uint(size), is_key_frame, uint64(duration))
		if err != nil {
			fmt.Println(err)
		}
	}

	return nil
}

func cameraHandler(ws *websocket.Conn) {
	//fmt.Printf("Camera connected from %s\n", ws.RemoteAddr().String())
	fmt.Printf("Camera connected\n")

	select {
	case camera_ch <- ws:
		fmt.Println("camera start")
		<-err_ch
	case w := <-client_ch:
		fmt.Println("camera --> client")
		err := process(w, ws)
		err_ch <- err
	}

	fmt.Println("camera quit")
}

func clientHandler(ws *websocket.Conn) {
	//fmt.Printf("Client connected from %s\n", ws.RemoteAddr().String())
	fmt.Printf("Client connected\n")

	select {
	case client_ch <- ws:
		fmt.Println("client start")
		<-err_ch
	case w := <-camera_ch:
		fmt.Println("client --> camera")
		err := process(ws, w)
		err_ch <- err
	}

	fmt.Println("client quit")
}

func main() {
	http.Handle("/camera", NoOrigHandler{cameraHandler})
	http.Handle("/client", NoOrigHandler{clientHandler})

	fmt.Println("Server start")
	http.ListenAndServe(":8080", nil)
}
