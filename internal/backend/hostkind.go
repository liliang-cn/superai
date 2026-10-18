package backend

import (
	"os"
	"runtime"
	"strings"
)

// Kinds of host a SuperAI process can run on. A host is the box an agent
// lives in; the agents on it (this SuperAI, openclaw, a coding CLI) are
// something else, and one host can carry several.
const (
	HostKindPod       = "pod"
	HostKindContainer = "container"
	HostKindMac       = "mac"
	HostKindVM        = "vm"
	HostKindMetal     = "metal"
)

// HostIdentity is what a process knows about the host it runs on.
type HostIdentity struct {
	// Name is the host's own name: the pod's name, the machine's hostname.
	Name string `json:"name"`
	Kind string `json:"kind"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// Parent is the host this one runs inside, when it knows: a pod's k3s
	// node ($SUPERAI_NODE). Empty for a top-level host.
	Parent string `json:"parent,omitempty"`
}

// hostProbe is everything DetectHost looks at, so tests can stand in for a
// pod, a VM or a Mac without being one.
type hostProbe struct {
	goos     string
	goarch   string
	hostname func() (string, error)
	getenv   func(string) string
	exists   func(string) bool
	read     func(string) string
}

func realProbe() hostProbe {
	return hostProbe{
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		hostname: os.Hostname,
		getenv:   os.Getenv,
		exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		read: func(p string) string {
			b, err := os.ReadFile(p)
			if err != nil {
				return ""
			}
			return string(b)
		},
	}
}

// DetectHost says what this process runs on. SUPERAI_HOST_KIND overrides the
// guess, and SUPERAI_HOST_NAME the name, for a box the guess gets wrong.
func DetectHost() HostIdentity { return detectHost(realProbe()) }

func detectHost(p hostProbe) HostIdentity {
	name, _ := p.hostname()
	h := HostIdentity{Name: name, OS: p.goos, Arch: p.goarch, Kind: hostKind(p)}
	if h.Kind == HostKindPod {
		h.Parent = strings.TrimSpace(p.getenv("SUPERAI_NODE"))
	}
	if k := strings.ToLower(strings.TrimSpace(p.getenv("SUPERAI_HOST_KIND"))); validHostKind(k) {
		h.Kind = k
	}
	if n := strings.TrimSpace(p.getenv("SUPERAI_HOST_NAME")); n != "" {
		h.Name = n
	}
	return h
}

func validHostKind(k string) bool {
	switch k {
	case HostKindPod, HostKindContainer, HostKindMac, HostKindVM, HostKindMetal:
		return true
	}
	return false
}

func hostKind(p hostProbe) string {
	// Inside first: a pod or a container on a Mac or a VM is that pod or
	// container, and its parent is the other thing.
	if p.getenv("KUBERNETES_SERVICE_HOST") != "" {
		return HostKindPod
	}
	if p.exists("/.dockerenv") || p.exists("/run/.containerenv") || p.getenv("container") != "" {
		return HostKindContainer
	}
	switch p.goos {
	case "darwin":
		return HostKindMac
	case "linux":
		if linuxIsVM(p) {
			return HostKindVM
		}
	}
	return HostKindMetal
}

// virtVendors are what a hypervisor writes into the DMI tables, lower case.
var virtVendors = []string{"qemu", "kvm", "vmware", "virtualbox", "xen", "bochs", "parallels", "hyper-v", "virtual machine", "apple virtualization", "proxmox", "openstack", "amazon ec2", "google compute engine"}

func linuxIsVM(p hostProbe) bool {
	dmi := strings.ToLower(p.read("/sys/class/dmi/id/sys_vendor") + " " + p.read("/sys/class/dmi/id/product_name"))
	for _, v := range virtVendors {
		if strings.Contains(dmi, v) {
			return true
		}
	}
	// x86 guests see the hypervisor bit; arm guests usually say so in DMI.
	for _, line := range strings.Split(p.read("/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(line, "flags") && strings.Contains(line, " hypervisor") {
			return true
		}
	}
	return false
}
