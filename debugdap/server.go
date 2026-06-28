package debugdap

import "net"

// ListenAndServe accepts DAP connections on addr (e.g. ":4711") and serves each
// with cfg until the listener closes. Each connection is an independent debug
// session, so one client debugs one program at a time over its connection.
func ListenAndServe(addr string, cfg Config) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return Accept(ln, cfg)
}

// Accept serves DAP connections from ln until it closes. Exposed so a host can
// listen itself (e.g. on a chosen port) and hand the listener over.
func Accept(ln net.Listener, cfg Config) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer conn.Close()
			_ = Serve(conn, cfg)
		}()
	}
}
