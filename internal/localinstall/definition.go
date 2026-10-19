package localinstall

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"net"
	"strconv"
	"strings"
)

func validateDefinition(p Paths, o Options) (string, error) {
	host, port, err := net.SplitHostPort(o.Listen)
	ip := net.ParseIP(host)
	n, parseErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || parseErr != nil || n < 1 || n > 65535 {
		return "", errors.New("installed core requires a fixed explicit loopback listener")
	}
	if strings.ContainsAny(o.Listen+o.PublicOrigin+p.Root+p.Data, "\x00\r\n") {
		return "", errors.New("invalid service option")
	}
	_, err = core.NewHTTPHandler(nil, "http://"+o.Listen, o.PublicOrigin, nil)
	return port, err
}
