package collectors

import (
	"fmt"
	"strconv"

	"github.com/StackExchange/scollector/metadata"
	"github.com/StackExchange/scollector/opentsdb"
	"github.com/StackExchange/scollector/util"
	"github.com/StackExchange/vsphere"
)

// Vsphere registers a vSphere collector.
func Vsphere(user, pwd, host string) {
	collectors = append(collectors, &IntervalCollector{
		F: func() (opentsdb.MultiDataPoint, error) {
			return c_vsphere(user, pwd, host)
		},
		name: fmt.Sprintf("vsphere-%s", host),
	})
}

func c_vsphere(user, pwd, host string) (opentsdb.MultiDataPoint, error) {
	v, err := vsphere.Connect(host, user, pwd)
	if err != nil {
		return nil, err
	}
	var md opentsdb.MultiDataPoint
	if err := vsphereHost(v, &md); err != nil {
		return nil, err
	}
	if err := vsphereDatastore(v, &md); err != nil {
		return nil, err
	}
	return md, nil
}

func vsphereDatastore(v *vsphere.Vsphere, md *opentsdb.MultiDataPoint) error {
	res, err := v.Info("Datastore", []string{
		"name",
		"summary.capacity",
		"summary.freeSpace",
	})
	if err != nil {
		return err
	}
	var Error error
	for _, r := range res {
		var name string
		for _, p := range r.Props {
			if p.Name == "name" {
				name = p.Val.Inner
				break
			}
		}
		if name == "" {
			Error = fmt.Errorf("vsphere: empty name")
			continue
		}
		tags := opentsdb.TagSet{
			"disk": name,
			"host": "",
		}
		var diskTotal, diskFree int64
		for _, p := range r.Props {
			switch p.Val.Type {
			case "xsd:long", "xsd:int", "xsd:short":
				i, err := strconv.ParseInt(p.Val.Inner, 10, 64)
				if err != nil {
					Error = fmt.Errorf("vsphere bad integer:", p.Val.Inner)
					continue
				}
				switch p.Name {
				case "summary.capacity":
					Add(md, osDiskTotal, i, tags, metadata.Gauge, metadata.Bytes, "")
					Add(md, "vsphere.disk.space_total", i, tags, metadata.Gauge, metadata.Bytes, "")
					diskTotal = i
				case "summary.freeSpace":
					Add(md, "vsphere.disk.space_free", i, tags, metadata.Gauge, metadata.Bytes, "")
					diskFree = i
				}
			}
		}
		if diskTotal > 0 && diskFree > 0 {
			diskUsed := diskTotal - diskFree
			Add(md, "vsphere.disk.space_used", diskUsed, tags, metadata.Gauge, metadata.Bytes, "")
			Add(md, osDiskUsed, diskUsed, tags, metadata.Gauge, metadata.Bytes, "")
			Add(md, osDiskPctFree, float64(diskFree)/float64(diskTotal)*100, tags, metadata.Gauge, metadata.Pct, "")
		}
	}
	return Error
}

func vsphereHost(v *vsphere.Vsphere, md *opentsdb.MultiDataPoint) error {
	res, err := v.Info("HostSystem", []string{
		"name",
		"summary.hardware.numCpuCores",
		"summary.hardware.cpuMhz",
		"summary.hardware.memorySize", // bytes
		"summary.hardware.numCpuCores",
		"summary.quickStats.overallCpuUsage",    // MHz
		"summary.quickStats.overallMemoryUsage", // MB
	})
	if err != nil {
		return err
	}
	var Error error
	for _, r := range res {
		var name string
		for _, p := range r.Props {
			if p.Name == "name" {
				name = util.Clean(p.Val.Inner)
				break
			}
		}
		if name == "" {
			Error = fmt.Errorf("vsphere: empty name")
			continue
		}
		tags := opentsdb.TagSet{
			"host": name,
		}
		var memTotal, memUsed int64
		var cpuMhz, cpuCores, cpuUse int64
		for _, p := range r.Props {
			switch p.Val.Type {
			case "xsd:long", "xsd:int", "xsd:short":
				i, err := strconv.ParseInt(p.Val.Inner, 10, 64)
				if err != nil {
					Error = fmt.Errorf("vsphere bad integer:", p.Val.Inner)
					continue
				}
				switch p.Name {
				case "summary.hardware.memorySize":
					Add(md, osMemTotal, i, tags, metadata.Gauge, metadata.Bytes, "")
					memTotal = i
				case "summary.quickStats.overallMemoryUsage":
					memUsed = i * 1024 * 1024
					Add(md, osMemUsed, memUsed, tags, metadata.Gauge, metadata.Bytes, "")
				case "summary.hardware.cpuMhz":
					cpuMhz = i
				case "summary.quickStats.overallCpuUsage":
					cpuUse = i
					Add(md, "vsphere.cpu", cpuUse, opentsdb.TagSet{"host": name, "type": "usage"}, metadata.Gauge, metadata.MHz, "")
				case "summary.hardware.numCpuCores":
					cpuCores = i
				}
			}
		}
		if memTotal > 0 && memUsed > 0 {
			memFree := memTotal - memUsed
			Add(md, osMemFree, memFree, tags, metadata.Gauge, metadata.Bytes, "")
			Add(md, osMemPctFree, float64(memFree)/float64(memTotal)*100, tags, metadata.Gauge, metadata.Pct, "")
		}
		if cpuMhz > 0 && cpuUse > 0 && cpuCores > 0 {
			cpuTotal := cpuMhz * cpuCores
			Add(md, "vsphere.cpu", cpuTotal-cpuUse, opentsdb.TagSet{"host": name, "type": "idle"}, metadata.Gauge, metadata.MHz, "")
			Add(md, "vsphere.cpu.pct", float64(cpuUse)/float64(cpuTotal)*100, tags, metadata.Gauge, metadata.Pct, "")
		}
	}
	return Error
}
