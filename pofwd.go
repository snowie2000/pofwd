/*
   Pofwd -- A network port forwarding program
   Copyright (C) 2016 Star Brilliant <m13253@hotmail.com>

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU General Public License for more details.

   You should have received a copy of the GNU General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

func main() {
	confPath := "pofwd.conf"
	if len(os.Args) == 2 {
		if os.Args[1] == "--help" {
			printUsage()
			os.Exit(0)
		} else {
			confPath = os.Args[1]
		}
	} else if len(os.Args) == 5 {
		if err := startForwarding(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
			log.Fatalln(err)
		}
		<-make(chan bool)
		os.Exit(0)
	} else if len(os.Args) != 1 {
		printUsage()
		os.Exit(1)
	}
	confFile, err := os.Open(confPath)
	if err != nil {
		log.Fatalln("cannot open configuration file:", err)
	}
	confScanner := bufio.NewScanner(confFile)
	confLineCount := 0
	for confScanner.Scan() {
		confLineCount++
		line := strings.SplitN(confScanner.Text(), "#", 2)[0]
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		} else if len(fields) != 4 {
			log.Fatalf("line %d: requires four parameters 'from protocol' 'from address' 'to protocol' 'to address'\n", confLineCount)
		} else if err = startForwarding(fields[0], fields[1], fields[2], fields[3]); err != nil {
			log.Fatalln(err)
		}
	}
	confFile.Close()
	if err = confScanner.Err(); err != nil {
		log.Fatalln("cannot read configuration file:", err)
	}
	<-make(chan bool)
}

func printUsage() {
	fmt.Printf("Usage: %s [CONFIG]\n   Or: %s <FROM PROTOCOL> <FROM ADDRESS> <TO PROTOCOL> <TO ADDRESS>\n\n  CONFIG\tConfiguration file [Default: pofwd.conf]\n\n", os.Args[0], os.Args[0])
}

func startForwarding(fromProtocol, fromAddress, toProtocol, toAddress string) error {
	if isPacketProtocol(fromProtocol) {
		return startForwardingPacket(fromProtocol, fromAddress, toProtocol, toAddress)
	}
	return startForwardingStream(fromProtocol, fromAddress, toProtocol, toAddress)
}

func startForwardingStream(fromProtocol, fromAddress, toProtocol, toAddress string) error {
	listener, err := net.Listen(fromProtocol, fromAddress)
	if err != nil {
		return err
	}
	log.Printf("serving on %s %s\n", listener.Addr().Network(), listener.Addr().String())
	go func() {
		for {
			connIn, err := listener.Accept()
			if err != nil {
				log.Printf("%s ? <-!-> %s %s <===> %s ? <---> %s %s\n", listener.Addr().Network(), listener.Addr().Network(), listener.Addr().String(), toProtocol, toProtocol, toAddress)
				if errNet, ok := err.(net.Error); ok {
					if errNet.Temporary() {
						log.Println(err)
						continue
					}
				}
				log.Fatalln(err)
			}
			go func() {
				connOut, err := net.Dial(toProtocol, toAddress)
				var connWait sync.WaitGroup
				connWait.Add(2)
				if err != nil {
					log.Printf("%s %s <---> %s %s <===> %s ? <-!-> %s %s\n", connIn.RemoteAddr().Network(), connIn.RemoteAddr().String(), connIn.LocalAddr().Network(), connIn.LocalAddr().String(), toProtocol, toProtocol, toAddress)
					log.Println(err)
					connIn.Close()
					return
				}
				log.Printf("%s %s <---> %s %s <===> %s %s <---> %s %s\n", connIn.RemoteAddr().Network(), connIn.RemoteAddr().String(), connIn.LocalAddr().Network(), connIn.LocalAddr().String(), connOut.LocalAddr().Network(), connOut.LocalAddr().String(), connOut.RemoteAddr().Network(), connOut.RemoteAddr().String())
				go func() {
					var err error
					var packetLen int
					buffer := make([]byte, 65537)
					if isPacketProtocol(toProtocol) {
						for {
							_, err = io.ReadFull(connIn, buffer[:2])
							if err != nil {
								break
							}
							packetLen = (int(buffer[0]) << 8) | int(buffer[1])
							if packetLen > 65535 {
								err = &tooLargePacketError{
									Size: packetLen,
								}
								break
							}
							_, err = io.ReadFull(connIn, buffer[2:2+packetLen])
							if err != nil {
								break
							}
							_, err = connOut.Write(buffer[2 : 2+packetLen])
							if err != nil {
								break
							}
						}
					} else {
						for {
							packetLen, err = connIn.Read(buffer)
							if err != nil {
								break
							}
							_, err = connOut.Write(buffer[:packetLen])
							if err != nil {
								break
							}
						}
					}
					if err == io.EOF {
						log.Printf("%s %s <---> %s %s ==X=> %s %s <---> %s %s\n", connIn.RemoteAddr().Network(), connIn.RemoteAddr().String(), connIn.LocalAddr().Network(), connIn.LocalAddr().String(), connOut.LocalAddr().Network(), connOut.LocalAddr().String(), connOut.RemoteAddr().Network(), connOut.RemoteAddr().String())
					} else {
						log.Printf("%s %s <---> %s %s ==!=> %s %s <---> %s %s\n", connIn.RemoteAddr().Network(), connIn.RemoteAddr().String(), connIn.LocalAddr().Network(), connIn.LocalAddr().String(), connOut.LocalAddr().Network(), connOut.LocalAddr().String(), connOut.RemoteAddr().Network(), connOut.RemoteAddr().String())
						log.Println(err)
					}
					if connInTCP, ok := connIn.(*net.TCPConn); ok {
						connInTCP.CloseRead()
					}
					if connOutTCP, ok := connOut.(*net.TCPConn); ok {
						connOutTCP.CloseWrite()
					} else {
						connOut.Close()
					}
					connWait.Done()
				}()
				go func() {
					var err error
					var packetLen int
					buffer := make([]byte, 65537)
					if isPacketProtocol(toProtocol) {
						for {
							connOut.SetReadDeadline(time.Now().Add(180 * time.Second))
							packetLen, err = connOut.Read(buffer[2:])
							if err != nil {
								break
							}
							buffer[0], buffer[1] = byte(packetLen>>8), byte(packetLen)
							_, err = connIn.Write(buffer[:2+packetLen])
							if err != nil {
								break
							}
						}
					} else {
						for {
							packetLen, err = connOut.Read(buffer)
							if err != nil {
								break
							}
							_, err = connIn.Write(buffer[:packetLen])
							if err != nil {
								break
							}
						}
					}
					if err == io.EOF {
						log.Printf("%s %s <---> %s %s <=X== %s %s <---> %s %s\n", connIn.RemoteAddr().Network(), connIn.RemoteAddr().String(), connIn.LocalAddr().Network(), connIn.LocalAddr().String(), connOut.LocalAddr().Network(), connOut.LocalAddr().String(), connOut.RemoteAddr().Network(), connOut.RemoteAddr().String())
					} else {
						log.Printf("%s %s <---> %s %s <=!== %s %s <---> %s %s\n", connIn.RemoteAddr().Network(), connIn.RemoteAddr().String(), connIn.LocalAddr().Network(), connIn.LocalAddr().String(), connOut.LocalAddr().Network(), connOut.LocalAddr().String(), connOut.RemoteAddr().Network(), connOut.RemoteAddr().String())
						log.Println(err)
					}
					if connOutTCP, ok := connOut.(*net.TCPConn); ok {
						connOutTCP.CloseRead()
					}
					if connInTCP, ok := connIn.(*net.TCPConn); ok {
						connInTCP.CloseWrite()
					} else {
						connIn.Close()
					}
					connWait.Done()
				}()
				connWait.Wait()
				log.Printf("%s %s <---> %s %s <=X=> %s %s <---> %s %s\n", connIn.RemoteAddr().Network(), connIn.RemoteAddr().String(), connIn.LocalAddr().Network(), connIn.LocalAddr().String(), connOut.LocalAddr().Network(), connOut.LocalAddr().String(), connOut.RemoteAddr().Network(), connOut.RemoteAddr().String())
				if connInTCP, ok := connIn.(*net.TCPConn); ok {
					connInTCP.Close()
				}
				if connOutTCP, ok := connOut.(*net.TCPConn); ok {
					connOutTCP.Close()
				}
			}()
		}
	}()
	return nil
}

func startForwardingPacket(fromProtocol, fromAddress, toProtocol, toAddress string) error {
	_, err := Forward(fromAddress, toAddress, DefaultTimeout)
	return err
}

func isPacketProtocol(protocolName string) bool {
	switch strings.ToLower(protocolName) {
	case "udp", "udp4", "udp6", "ip", "ip4", "ip6", "unixgram":
		return true
	default: // "tcp", "tcp4", "tcp6", "unix", "unixpacket"
		return false
	}
}

type tooLargePacketError struct {
	Size int
}

func (e *tooLargePacketError) Error() string {
	return fmt.Sprintf("packet too large (%d > 65535)", e.Size)
}
