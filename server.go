package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/miekg/dns"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	hostsFilePath   = "hosts.json"
	version         = "0.2.1"
	defaultResolver = "1.1.1.1"
)

var (
	mutex   sync.Mutex
	logger  *log.Logger
	logChan = make(chan string, 1024)
)

// DnsRecord represents a DNS record with host and ip
type DnsRecord struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
}

func init() {
	// Initialize the logger to write to stdout
	logger = log.New(os.Stdout, "", 0)

	go func() {
		for logMsg := range logChan {
			logger.Print(logMsg)
		}
	}()

	// Define and parse command-line flags
	flag.Bool("version", false, "Print version information")
	flag.Parse()
}

func printVersion() {
	fmt.Println(fmt.Sprintf("godns v%s", version))
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

	decoder := json.NewDecoder(file)
	records := make(map[string]string)
	err = decoder.Decode(&records)
	if err != nil {
		return nil, err
	}

	return records, nil
}

func decodeDNSMessage(data []byte, messageType string) string {
	var result string

	dnsMsg := new(dns.Msg)
	var err error
	switch messageType {
	case "request":
		err = dnsMsg.Unpack(data)
	case "response":
		err = dnsMsg.Unpack(data)
	default:
		result = fmt.Sprintf("invalid message type: %s", messageType)
		return result
	}

	if err != nil {
		result = fmt.Sprintf("error decoding DNS %s: %v", messageType, err)
	} else {
		// Convert DNS message to a human-readable string
		result = dnsMsg.String()
	}

	return result
}

func logRequest(data []byte, addr *net.UDPAddr) {
	dnsMsg := decodeDNSMessage(data, "request")
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	requestLog := fmt.Sprintf("%s %s#%d: %s", timestamp, addr.IP.String(), addr.Port, dnsMsg)
	logger.Print(requestLog)
}

func logResponse(response []byte, addr *net.UDPAddr) {
	dnsMsg := decodeDNSMessage(response, "response")
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	responseLog := fmt.Sprintf("%s %s#%d: %s", timestamp, addr.IP.String(), addr.Port, dnsMsg)
	logger.Print(responseLog)
}

func handleRequest(data []byte, records map[string]string, addr *net.UDPAddr, defaultResolver string, id uint16) []byte {
	logRequest(data, addr)

	var dnsMsg dns.Msg
	if err := dnsMsg.Unpack(data); err != nil {
		return nil
	}

	if len(dnsMsg.Question) == 0 {
		return nil
	}

	host := strings.TrimSuffix(dnsMsg.Question[0].Name, ".")

	response := new(dns.Msg)
	response.SetReply(&dnsMsg)
	response.Authoritative = true
	response.Id = id

	ip, found := records[host]
	if found {
		rr := new(dns.A)
		rr.Hdr = dns.RR_Header{Name: dnsMsg.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}
		rr.A = net.ParseIP(ip).To4()
		response.Answer = append(response.Answer, rr)
	} else {
		// If the host is not found, query using defaultResolver
		client := &dns.Client{}
		result, _, err := client.Exchange(&dns.Msg{
			MsgHdr: dns.MsgHdr{Id: id, RecursionDesired: true},
			Question: []dns.Question{
				{Name: dnsMsg.Question[0].Name, Qtype: dns.TypeA, Qclass: dns.ClassINET},
			},
		}, defaultResolver+":53")
		if err != nil {
			fmt.Println("error querying default resolver:", err)
			response.Rcode = dns.RcodeServerFailure
		} else {
			response = result
		}
	}

	responseData, err := response.Pack()
	if err != nil {
		fmt.Println("error calling response.Pack():", err)
		return nil
	}

	logResponse(responseData, addr)
	return responseData
}

func worker(serverConn *net.UDPConn, data []byte, addr *net.UDPAddr, records map[string]string, defaultResolver string, id uint16) {
	response := handleRequest(data, records, addr, defaultResolver, id)

	// Send the response using the same connection
	_, err := serverConn.WriteToUDP(response, addr)
	if err != nil {
		fmt.Println("error sending response:", err)
	}
}

func main() {
	// Check for version flag and print version
	if flag.Lookup("version").Value.(flag.Getter).Get().(bool) {
		printVersion()
	}

	// Load hosts file
	dnsRecords, err := loadHosts()
	if err != nil {
		fmt.Println("error loading hosts file:", err)
		os.Exit(1)
	}

	// Create a UDP address to listen on port 53
	serverAddr, err := net.ResolveUDPAddr("udp", ":53")
	if err != nil {
		fmt.Println("error resolving address:", err)
		os.Exit(1)
	}

	// Create a UDP listener
	serverConn, err := net.ListenUDP("udp", serverAddr)
	if err != nil {
		fmt.Println("error listening:", err)
		os.Exit(1)
	}
	defer serverConn.Close()

	logger.Print("godns listening on :53...")

	for {
		// Wait for a DNS request
		buffer := make([]byte, 1024)
		_, clientAddr, err := serverConn.ReadFromUDP(buffer)
		if err != nil {
			fmt.Println("error reading data:", err)
			continue
		}

		id := binary.BigEndian.Uint16(buffer[:2])

		// Handle the DNS request in a separate goroutine
		go worker(serverConn, buffer, clientAddr, dnsRecords, defaultResolver, id)
	}
}
