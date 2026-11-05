package main

import (
	"fmt"
	"strings"
)

// Human-friendly touches. Pure functions, no OS access.

// portHints names what usually listens on a port. They are guesses (any
// program can use any port), so they are only shown for owners we can't see,
// and always worded as "probably".
var portHints = map[int]string{
	22: "SSH", 25: "mail (SMTP)", 53: "DNS", 67: "DHCP", 68: "DHCP client", 80: "a web server (HTTP)",
	111: "rpcbind", 123: "time sync (NTP)", 139: "Windows file sharing", 443: "a web server (HTTPS)",
	445: "Windows file sharing", 546: "DHCPv6 client", 631: "printing (CUPS)", 1883: "MQTT",
	2049: "NFS file sharing", 2375: "Docker API", 3000: "a dev server", 3306: "MySQL", 3389: "remote desktop (RDP)",
	5000: "a dev server", 5173: "Vite dev server", 5353: "local network discovery (mDNS)",
	5355: "name lookup (LLMNR)", 5432: "PostgreSQL", 5672: "RabbitMQ", 5900: "remote desktop (VNC)",
	6379: "Redis", 8000: "a dev server", 8080: "a web server", 8384: "Syncthing", 8888: "Jupyter",
	9090: "Prometheus or a web UI", 9200: "Elasticsearch", 11434: "Ollama", 27017: "MongoDB",
}

// tildePath shortens a path: $HOME becomes ~, and very long paths keep their tail.
func tildePath(p, home string) string {
	if home != "" && home != "/" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+"/") {
			p = "~" + p[len(home):]
		}
	}
	if len(p) > 32 {
		parts := strings.Split(p, "/")
		if n := len(parts); n > 3 {
			p = "…/" + strings.Join(parts[n-2:], "/")
		}
	}
	return p
}

// shortCmd makes a command line readable: the executable by name only, path
// arguments tilde-shortened. "/usr/bin/node /home/me/app/server.js" -> "node ~/app/server.js".
func shortCmd(cmd, home string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return cmd
	}
	if i := strings.LastIndexByte(fields[0], '/'); i >= 0 && !strings.HasPrefix(fields[0], "[") {
		fields[0] = fields[0][i+1:]
	}
	for i := 1; i < len(fields); i++ {
		if strings.HasPrefix(fields[i], "/") {
			fields[i] = tildePath(fields[i], home)
		}
	}
	return strings.Join(fields, " ")
}

// niceCwd returns where a process lives, or "" when that adds no information.
func niceCwd(cwd, home string) string {
	if cwd == "" || cwd == "/" || cwd == home {
		return ""
	}
	return tildePath(cwd, home)
}

func fmtAge(sec float64) string {
	switch s := int(sec); {
	case sec < 0:
		return ""
	case s < 60:
		return fmt.Sprintf("up %ds", s)
	case s < 3600:
		return fmt.Sprintf("up %dm", s/60)
	case s < 86400:
		return fmt.Sprintf("up %dh", s/3600)
	default:
		return fmt.Sprintf("up %dd", s/86400)
	}
}

// stripArgv0 drops the program name from a command line when the process
// column already shows it ("python -m app" -> "-m app"). comm is truncated to
// 15 characters by the kernel, so a prefix match counts.
func stripArgv0(name, cmd string) string {
	first, rest, _ := strings.Cut(cmd, " ")
	if first == name || (len(name) >= 15 && strings.HasPrefix(first, name)) {
		return rest
	}
	return cmd
}
