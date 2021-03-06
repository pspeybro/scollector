package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/StackExchange/scollector/collect"
	"github.com/StackExchange/scollector/collectors"
	"github.com/StackExchange/scollector/metadata"
	"github.com/StackExchange/scollector/opentsdb"
	"github.com/StackExchange/scollector/util"
	"github.com/StackExchange/slog"
)

// These constants should remain in source control as their zero values.
const (
	// VersionDate should be set at build time as a date: 20140721184001.
	VersionDate uint64 = 0
	// VersionID should be set at build time as the most recent commit hash.
	VersionID string = ""
)

var (
	flagFilter          = flag.String("f", "", "Filters collectors matching this term. Works with all other arguments.")
	flagList            = flag.Bool("l", false, "List")
	flagPrint           = flag.Bool("p", false, "Print to screen instead of sending to a host")
	flagHost            = flag.String("h", "bosun", `bosun or OpenTSDB host. Ex: "http://tsdb.example.com:4242".`)
	flagColDir          = flag.String("c", "", `External collectors directory.`)
	flagBatchSize       = flag.Int("b", 0, "OpenTSDB batch size. Used for debugging bad data.")
	flagSNMP            = flag.String("s", "", "SNMP host to poll of the format: \"community@host[,community@host...]\".")
	flagICMP            = flag.String("i", "", "ICMP host to ping of the format: \"host[,host...]\".")
	flagVsphere         = flag.String("v", "", `vSphere host to poll of the format: "user:password@host[,user:password@host...]".`)
	flagFake            = flag.Int("fake", 0, "Generates X fake data points on the test.fake metric per second.")
	flagDebug           = flag.Bool("d", false, "Enables debug output.")
	flagJSON            = flag.Bool("j", false, "With -p enabled, prints JSON.")
	flagFullHost        = flag.Bool("u", false, `Enables full hostnames: doesn't truncate to first ".".`)
	flagDisableMetadata = flag.Bool("m", false, "Disable sending of metadata.")
	flagVersion         = flag.Bool("version", false, `Prints the version and exits.`)

	mains []func()
)

func readConf() {
	p, err := exePath()
	if err != nil {
		slog.Error(err)
		return
	}
	dir := filepath.Dir(p)
	p = filepath.Join(dir, "scollector.conf")
	b, err := ioutil.ReadFile(p)
	if err != nil {
		if *flagDebug {
			slog.Error(err)
		}
		return
	}
	for i, line := range strings.Split(string(b), "\n") {
		sp := strings.SplitN(line, "=", 2)
		if len(sp) != 2 {
			if *flagDebug {
				slog.Errorf("expected = in %v:%v", p, i+1)
			}
			continue
		}
		k := strings.TrimSpace(sp[0])
		v := strings.TrimSpace(sp[1])
		f := func(s *string) {
			*s = v
		}
		switch k {
		case "host":
			f(flagHost)
		case "filter":
			f(flagFilter)
		case "coldir":
			f(flagColDir)
		case "snmp":
			f(flagSNMP)
		case "icmp":
			f(flagICMP)
		case "vsphere":
			f(flagVsphere)
		default:
			if *flagDebug {
				slog.Errorf("unknown key in %v:%v", p, i+1)
			}
		}
	}
}

func main() {
	flag.Parse()
	if *flagPrint {
		slog.Set(&slog.StdLog{Log: log.New(os.Stdout, "", log.LstdFlags)})
	}
	if *flagVersion {
		fmt.Printf("scollector version %v (%v)\n", VersionDate, VersionID)
		os.Exit(0)
	}
	for _, m := range mains {
		m()
	}
	readConf()

	util.FullHostname = *flagFullHost
	util.Set()
	if *flagColDir != "" {
		collectors.InitPrograms(*flagColDir)
	}
	if *flagSNMP != "" {
		for _, s := range strings.Split(*flagSNMP, ",") {
			sp := strings.Split(s, "@")
			if len(sp) != 2 {
				slog.Fatal("invalid snmp string:", *flagSNMP)
			}
			collectors.SNMPIfaces(sp[0], sp[1])
			collectors.SNMPCisco(sp[0], sp[1])
		}
	}
	if *flagICMP != "" {
		for _, s := range strings.Split(*flagICMP, ",") {
			collectors.ICMP(s)
		}
	}
	if *flagVsphere != "" {
		for _, s := range strings.Split(*flagVsphere, ",") {
			sp := strings.SplitN(s, ":", 2)
			if len(sp) != 2 {
				slog.Fatal("invalid vsphere string:", *flagVsphere)
			}
			user := sp[0]
			idx := strings.LastIndex(sp[1], "@")
			if idx == -1 {
				slog.Fatal("invalid vsphere string:", *flagVsphere)
			}
			pwd := sp[1][:idx]
			host := sp[1][idx+1:]
			if len(user) == 0 || len(pwd) == 0 || len(host) == 0 {
				slog.Fatal("invalid vsphere string:", *flagVsphere)
			}
			collectors.Vsphere(user, pwd, host)
		}
	}
	if *flagFake > 0 {
		collectors.InitFake(*flagFake)
	}
	collect.Debug = *flagDebug
	c := collectors.Search(*flagFilter)
	for _, col := range c {
		col.Init()
	}
	u, err := parseHost()
	if *flagList {
		list(c)
		return
	} else if err != nil {
		slog.Fatal("invalid host:", *flagHost)
	}
	if !*flagDisableMetadata {
		if err := metadata.Init(u, *flagDebug); err != nil {
			slog.Fatal(err)
		}
	}
	if *flagPrint {
		collectors.DefaultFreq = time.Second * 3
		slog.Infoln("Set default frequency to", collectors.DefaultFreq)
	}
	cdp := collectors.Run(c)
	if u != nil && !*flagPrint {
		slog.Infoln("OpenTSDB host:", u)
		if err := collect.InitChan(u, "scollector", cdp); err != nil {
			slog.Fatal(err)
		}
		if VersionDate > 0 {
			if err := collect.Put("version", nil, VersionDate); err != nil {
				slog.Error(err)
			}
		}
		if *flagBatchSize > 0 {
			collect.BatchSize = *flagBatchSize
		}
		go func() {
			const maxMem = 500 * 1024 * 1024 // 500MB
			var m runtime.MemStats
			for _ = range time.Tick(time.Minute) {
				runtime.ReadMemStats(&m)
				if m.Alloc > maxMem {
					panic("memory max reached")
				}
			}
		}()
	} else {
		slog.Infoln("Outputting to screen")
		printPut(cdp)
	}
	select {}
}

func exePath() (string, error) {
	prog := os.Args[0]
	p, err := filepath.Abs(prog)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
	if err == nil {
		if !fi.Mode().IsDir() {
			return p, nil
		}
		err = fmt.Errorf("%s is directory", p)
	}
	if filepath.Ext(p) == "" {
		p += ".exe"
		fi, err := os.Stat(p)
		if err == nil {
			if !fi.Mode().IsDir() {
				return p, nil
			}
			err = fmt.Errorf("%s is directory", p)
		}
	}
	return "", err
}

func list(cs []collectors.Collector) {
	for _, c := range cs {
		fmt.Println(c.Name())
	}
}

func parseHost() (*url.URL, error) {
	if *flagHost == "" {
		return nil, fmt.Errorf("empty host")
	}
	if !strings.Contains(*flagHost, "//") {
		*flagHost = "http://" + *flagHost
	}
	return url.Parse(*flagHost)
}

func printPut(c chan *opentsdb.DataPoint) {
	for dp := range c {
		if *flagJSON {
			b, _ := json.Marshal(dp)
			slog.Info(string(b))
		} else {
			slog.Info(dp.Telnet())
		}
	}
}
