package collectors

import (
	"fmt"
	"net"
	"time"

	"github.com/StackExchange/scollector/metadata"
	"github.com/StackExchange/scollector/opentsdb"
	"github.com/tatsushid/go-fastping"
)

type response struct {
	addr *net.IPAddr
	rtt  time.Duration
}

// ICMP registers an ICMP collector a given host.
func ICMP(host string) {
	collectors = append(collectors, &IntervalCollector{
		F: func() (opentsdb.MultiDataPoint, error) {
			return c_icmp(host)
		},
		name: fmt.Sprintf("icmp-%s", host),
	})
}

func c_icmp(host string) (opentsdb.MultiDataPoint, error) {
	var md opentsdb.MultiDataPoint
	p := fastping.NewPinger()
	ra, err := net.ResolveIPAddr("ip4:icmp", host)
	if err != nil {
		return nil, err
	}
	p.AddIPAddr(ra)
	p.MaxRTT = time.Second * 5
	timeout := 1
	p.AddHandler("receive", func(addr *net.IPAddr, t time.Duration) {
		Add(&md, "ping.rtt", float64(t)/float64(time.Millisecond), opentsdb.TagSet{"dst_host": host}, metadata.Unknown, metadata.None, "")
		timeout = 0
	})
	if err := p.Run(); err != nil {
		return nil, err
	}
	Add(&md, "ping.timeout", timeout, opentsdb.TagSet{"dst_host": host}, metadata.Unknown, metadata.None, "")
	return md, nil
}
