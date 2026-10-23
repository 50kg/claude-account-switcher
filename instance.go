package main

import (
	"bufio"
	"log"
	"net"
	"strings"
	"time"
)

// Only one switcher may run. It holds instancePort; a second start says hello
// with its version there. The running one quits when the newcomer is newer,
// so starting a new version replaces the old one, and otherwise stays.
const instancePort = "127.0.0.1:47823"

const helloPrefix = "claude-account-switcher "

// lockInstance takes the single-instance port, taking it over from an older
// switcher that is running. It fails when a switcher of this version or a
// newer one is running.
func lockInstance(addr string) (net.Listener, bool) {
	if l, err := net.Listen("tcp", addr); err == nil {
		return l, true
	}
	switch hello(addr, version) {
	case "quit":
		log.Print("an older switcher is running, it is quitting")
	case "stay":
		log.Print("already running")
		return nil, false
	default:
		// Versions before 0.3.3 hold the port without answering.
		if !endOldSwitcher() {
			log.Print("already running")
			return nil, false
		}
		log.Print("ended a switcher from before 0.3.3")
	}
	// The old switcher finishes a running switch before it quits.
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if l, err := net.Listen("tcp", addr); err == nil {
			return l, true
		}
	}
	log.Print("the running switcher did not quit")
	return nil, false
}

// hello tells the switcher holding addr this version and returns its answer,
// "quit" or "stay", or "" when it does not answer.
func hello(addr, v string) string {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(helloPrefix + v + "\n")); err != nil {
		return ""
	}
	answer, _ := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(answer)
}

// serveInstance answers hellos on the lock and calls quit, once, when a newer
// version says hello.
func serveInstance(l net.Listener, quit func()) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		v, ok := strings.CutPrefix(line, helloPrefix)
		if ok && newerVersion(strings.TrimSpace(v), version) {
			conn.Write([]byte("quit\n"))
			conn.Close()
			log.Printf("version %s started, quitting", strings.TrimSpace(v))
			quit()
			return
		}
		conn.Write([]byte("stay\n"))
		conn.Close()
	}
}
