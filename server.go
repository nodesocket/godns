package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/miekg/dns"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	hostsFilePath   = "hosts.json"
	version         = "0.3.1"
	defaultResolver = "1.1.1.1"
)

var (
	mutex       sync.Mutex
	logger      *log.Logger
	logChan     = make(chan string, 1024)
	bufferPool  = sync.Pool{New: func() interface{} { return make([]byte, 1024) }}
	upstreamDNS = &dns.Client{Net: "udp", Timeout: 2 * time.Second}
)

type DnsRecord struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
}

func init() {
	logger = log.New(os.Stdout, "", 0)

	go func() {
		for logMsg := range logChan {
			logger.Print(logMsg)
		}
	}()
}

func printVersion() {
	fmt.Printf("godns v%s\n", version)
	os.Exit(0)
}

func loadHosts() (map[string]string, error) {
	mutex.Lock()
	defer mutex.Unlock()

	file, err := os.Open(hostsFilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	raw := make(map[string]string)
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}

	records := make(map[string]string)
	for k, v := range raw {
		records[strings.ToLower(strings.TrimSuffix(k, "."))] = v
	}
	return records, nil
}

func decodeDNSMessage(data []byte, messageType string) string {
	dnsMsg := new(dns.Msg)
	err := dnsMsg.Unpack(data)
	if err != nil {
		return fmt.Sprintf("error decoding DNS %s: %v", messageType, err)
	}
	return dnsMsg.String()
}

func logRequest(data []byte, addr *net.UDPAddr) {
	msg := decodeDNSMessage(data, "request")
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	logChan <- fmt.Sprintf("[%s] (%s:%d) REQUEST:\n%s", timestamp, addr.IP.String(), addr.Port, msg)
}

func logResponse(response []byte, addr *net.UDPAddr) {
	msg := decodeDNSMessage(response, "response")
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	logChan <- fmt.Sprintf("[%s] (%s:%d) RESPONSE:\n%s", timestamp, addr.IP.String(), addr.Port, msg)
}

func handleRequest(data []byte, records map[string]string, addr *net.UDPAddr, id uint16) []byte {
	logRequest(data, addr)

	var dnsMsg dns.Msg
	if err := dnsMsg.Unpack(data); err != nil || len(dnsMsg.Question) == 0 {
		return nil
	}

	q := dnsMsg.Question[0]
	host := strings.ToLower(strings.TrimSuffix(q.Name, "."))

	response := new(dns.Msg)
	response.SetReply(&dnsMsg)
	response.Authoritative = true
	response.Id = id

	ip, found := records[host]
	if found {
		parsedIP := net.ParseIP(ip)
		if parsedIP == nil {
			logChan <- fmt.Sprintf("Invalid IP in hosts file: %s", ip)
			response.Rcode = dns.RcodeServerFailure
		} else {
			rr := &dns.A{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    1,
				},
				A: parsedIP.To4(),
			}
			response.Answer = append(response.Answer, rr)
		}
	} else {
		fallbackMsg := &dns.Msg{
			MsgHdr: dns.MsgHdr{Id: id, RecursionDesired: true},
			Question: []dns.Question{
				{Name: q.Name, Qtype: dns.TypeA, Qclass: dns.ClassINET},
			},
		}
		result, _, err := upstreamDNS.Exchange(fallbackMsg, defaultResolver+":53")
		if err != nil {
			logChan <- fmt.Sprintf("Error querying upstream resolver: %v", err)
			response.Rcode = dns.RcodeServerFailure
		} else {
			response = result
		}
	}

	responseData, err := response.Pack()
	if err != nil {
		logChan <- fmt.Sprintf("Error packing DNS response: %v", err)
		return nil
	}

	logResponse(responseData, addr)
	return responseData
}

func worker(serverConn *net.UDPConn, data []byte, addr *net.UDPAddr, records map[string]string, id uint16) {
	response := handleRequest(data, records, addr, id)
	if response != nil {
		if _, err := serverConn.WriteToUDP(response, addr); err != nil {
			logChan <- fmt.Sprintf("Error sending response: %v", err)
		}
	}
}

func main() {
	showVersion := flag.Bool("version", false, "Print version information")
	flag.Parse()
	if *showVersion {
		printVersion()
	}

	dnsRecords, err := loadHosts()
	if err != nil {
		fmt.Println("Error loading hosts file:", err)
		os.Exit(1)
	}

	serverAddr, err := net.ResolveUDPAddr("udp", ":53")
	if err != nil {
		fmt.Println("Error resolving address:", err)
		os.Exit(1)
	}

	serverConn, err := net.ListenUDP("udp", serverAddr)
	if err != nil {
		fmt.Println("Error listening:", err)
		os.Exit(1)
	}
	defer serverConn.Close()

	logger.Print("godns listening on :53...")

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		logChan <- "Shutting down..."
		cancel()
		serverConn.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		default:
			buffer := bufferPool.Get().([]byte)
			n, clientAddr, err := serverConn.ReadFromUDP(buffer)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logChan <- fmt.Sprintf("Error reading data: %v", err)
				bufferPool.Put(buffer)
				continue
			}

			data := make([]byte, n)
			copy(data, buffer[:n])
			bufferPool.Put(buffer)

			id := binary.BigEndian.Uint16(data[:2])

			wg.Add(1)
			go func() {
				defer wg.Done()
				worker(serverConn, data, clientAddr, dnsRecords, id)
			}()
		}
	}
}
