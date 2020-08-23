package proxy

import (
	"fmt"
	"hiteshkotian/ssl-tunnel/handler"
	"hiteshkotian/ssl-tunnel/logging"
	"net"
	"time"
)

// Server structure represents the main proxy instance
type Server struct {
	// Name of the server
	name string
	// Port the server is listening for incoming requests
	port int
	// Maximum number of concurrent connections
	// that can be processed at a given time
	maxConnectionCount int
	// incoming network listener
	listener net.Listener
	// connectionHandler channel. This channel is used for piping the
	// incoming connections to the appropriate handler
	connectHandler chan net.Conn
	// Connection limiter. This channel ensures that at a given time the
	// configured number of requests are being processed.
	sem chan bool
}

// New creats a new instance of the proxy
func New(name string, port, maxConnectionCount int) *Server {

	proxy := &Server{name: name, port: port,
		maxConnectionCount: maxConnectionCount}
	proxy.connectHandler = make(chan net.Conn)
	proxy.sem = make(chan bool, proxy.maxConnectionCount)

	return proxy
}

// NewFromConfig reads the provided config file and
// returns a proxy instance
func NewFromConfig(configPath string) (*Server, error) {
	// TODO to implement this
	return nil, nil
}

// Start starts the server and accepts incoming client requests
func (server *Server) Start() error {
	var err error
	logging.Info("Starting Proxy Server")
	server.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", server.port))
	if err != nil {
		logging.Error("Unable to start tcp server: ", err)
		return err
	}

	return server.ServeTCP()
}

// ServeTCP will start  accepting TCP connections and will
// respond to client's connection.
func (server *Server) ServeTCP() error {

	// Start the connection handler
	go server.startHandler()

	for {
		conn, err := server.listener.Accept()

		if err != nil {
			logging.Error("Erorr while reading incoming request", err)
			return err
		}

		logging.Debug("received connection request from %s",
			conn.RemoteAddr().String())

		// Set all the required timeouts
		conn.SetReadDeadline(
			time.Now().Add(10 * time.Second))
		conn.SetWriteDeadline(
			time.Now().Add(30 * time.Second))

		server.connectHandler <- conn
	}
}

// startHandler function starts listening for incoming TCP
// connection and handles the incoming requests
func (server *Server) startHandler() {
	for {
		select {
		case conn, more := <-server.connectHandler:
			if !more {
				return
			}
			server.sem <- true
			go server.handleRequest(conn, server.sem)
		}
	}
}

// HandleRequest implementation for TCP Handler.
// This function will accept all incoming TCP requests
// and serialize it to a request object if it is a valid request.
// In case of a serialization issue, the handler will return
// an appropriate error code to the client.
func (server *Server) handleRequest(conn net.Conn, sem chan bool) error {

	logging.Debug("Processing incoming client request")
	request := handler.NewRequest(conn)
	defer request.Close()

	err := server.handleInitial(request)
	if err != nil {
		server.sendSocksError(request)
		<-sem
		return nil
	}

	// Writ accept
	response := []byte{0x05, 0x00}
	conn.Write(response)

	// Wait for response
	err = server.handleConnectRequest(request)
	if err != nil {
		logging.Error("Error handling connect request : ", err)
		server.sendSocksError(request)
		<-sem
		return nil
	}

	outboundHandler := handler.OutboundHandler{}
	err = outboundHandler.HandleRequest(request)
	if err != nil {
		logging.Error("Error while sending request : %s\n", err)
		server.sendSocksError(request)
		<-sem
		return nil
	}

	<-sem
	return nil
}

func (server *Server) handleInitial(request *handler.Request) error {
	data := make([]byte, 20)
	n, e := request.Read(data)
	if e != nil {
		return e
	}

	version := data[0]
	authCt := data[1]
	logging.Debug("Total num : %d", n)
	logging.Debug("Received connect with version : %d", version)
	logging.Debug("Received connect with auth ct : %d", authCt)

	if version != 0x05 {
		logging.Error("Version mismatch",
			fmt.Errorf("Version expeted was 0x05 but received %d", version))
	} else {
		logging.Debug("Version matched!!!")
	}
	for i := 0; i < n; i++ {
		logging.Debug("0x%02x ", data[i])
	}
	request.SetState(handler.INITIALIZING)

	return nil
}

func (server *Server) handleConnectRequest(request *handler.Request) error {
	data := make([]byte, 200)
	_, err := request.Read(data)

	if err != nil {
		logging.Error("error reading request", err)
		return err
	}

	connect, err := GetSocketRequestDeserialized(data)
	if err != nil {
		logging.Error("Connect request error", err)
		return err
	}

	var outConnection net.Conn
	if connect.atype == 0x01 {
		ip := fmt.Sprintf("%d.%d.%d.%d", connect.destaddr[0],
			connect.destaddr[1], connect.destaddr[2], connect.destaddr[3])
		request.SetOutboundIP(ip)
		outConnection, err = net.Dial("tcp", fmt.Sprintf("%s:%d", ip, connect.destport))
		if err != nil {
			return err
		}

		request.SetOutboundConnection(outConnection)
	} else if connect.atype == 0x03 {
		addr, err := net.LookupHost(string(connect.destaddr))
		if err != nil {
			return err
		}

		host := fmt.Sprintf("%s:%d", addr[0], connect.destport)
		outConnection, err = net.Dial("tcp", host)
		if err != nil {
			return err
		}

		request.SetOutboundConnection(outConnection)
	} else if connect.atype == 0x04 {
		ip := net.IP(connect.destaddr)
		ipAddr := ip.String()
		// logging.Debug("Connecting to [%s]:%d\n", ipAddr, connect.destport)
		request.SetOutboundIP(ipAddr)
		outConnection, err = net.Dial("tcp6", fmt.Sprintf("[%s]:%d", ipAddr, connect.destport))
		if err != nil {
			return err
		}
		request.SetOutboundConnection(outConnection)
	} else {
		// logging.Info("Connection type %d not supported", connect.atype)
		// return fmt.Errorf("Unsupported connection type")
		server.sendSocksConnectError(request, 0x08, &connect)
		return fmt.Errorf("Unsupported connection type")
	}

	request.SetOutboundPort(connect.destport)

	dest := connect.destaddr
	port := []byte{0x00, 0x50}

	resp := make([]byte, 4+len(dest)+len(port))
	resp[0] = 0x05
	resp[1] = 0x00
	resp[2] = 0x00
	resp[3] = byte(connect.atype)
	copy(resp[4:], dest)
	copy(resp[4+len(dest):], port)

	request.Write(resp)
	return nil
}

// Stop stops the server
func (server *Server) Stop() {
	// Closing Channel
	logging.Info("Stopping Proxy Server")
	close(server.connectHandler)
	server.listener.Close()
}

func (server *Server) sendSocksError(request *handler.Request) {
	// state := request.State()
	request.SetState(handler.ERROR)
	var errorStream []byte
	// switch state {
	// case handler.NEW:
	// Sending INIT error
	// Format :
	// +-----+-------+
	// | 1   |   1   |
	// +-----+-------+
	// | VER | STATE |
	// +-----+-------+
	errorStream = []byte{0x05, 0x01}
	// default:
	// Sending INIT error
	// Format :
	// +-----+--------+-----+---------+---------+
	// | 1   |   1    |  1  |  var    |   2     |
	// +-----+--------+-----+---------+---------+
	// | VER | STATUS | RSV | BNDADDR | BNDPORT |
	// +-----+--------+-----+---------+---------+

	// }
	request.Write(errorStream)
}

func (server *Server) sendSocksConnectError(request *handler.Request, status uint8, req *SockRequest) {
	request.SetState(handler.ERROR)

	var errorStream []byte
	// other := new(bytes.Buffer)
	errorStream = append(errorStream, 0x05)
	errorStream = append(errorStream, status)
	errorStream = append(errorStream, 0x00)
	errorStream = append(errorStream, req.destaddr...)
	errorStream = append(errorStream, 0x01)
	errorStream = append(errorStream, 0xBB)

	request.Write(errorStream)
}
