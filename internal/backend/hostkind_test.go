package backend

import "testing"

func probe(goos string, env map[string]string, files map[string]string) hostProbe {
	return hostProbe{
		goos:     goos,
		goarch:   "arm64",
		hostname: func() (string, error) { return "box", nil },
		getenv:   func(k string) string { return env[k] },
		exists:   func(p string) bool { _, ok := files[p]; return ok },
		read:     func(p string) string { return files[p] },
	}
}

func TestDetectHost(t *testing.T) {
	cases := []struct {
		name   string
		p      hostProbe
		kind   string
		parent string
		host   string
	}{
		{"pod on a k3s node", probe("linux", map[string]string{"KUBERNETES_SERVICE_HOST": "10.43.0.1", "SUPERAI_NODE": "orange2"}, nil), HostKindPod, "orange2", "box"},
		{"docker container", probe("linux", nil, map[string]string{"/.dockerenv": ""}), HostKindContainer, "", "box"},
		{"podman container", probe("linux", nil, map[string]string{"/run/.containerenv": ""}), HostKindContainer, "", "box"},
		{"mac", probe("darwin", nil, nil), HostKindMac, "", "box"},
		{"proxmox vm", probe("linux", nil, map[string]string{"/sys/class/dmi/id/sys_vendor": "QEMU\n", "/sys/class/dmi/id/product_name": "Standard PC (Q35 + ICH9, 2009)\n"}), HostKindVM, "", "box"},
		{"lima on apple virtualization", probe("linux", nil, map[string]string{"/sys/class/dmi/id/sys_vendor": "Apple Inc.\n", "/sys/class/dmi/id/product_name": "Apple Virtualization Generic Platform\n"}), HostKindVM, "", "box"},
		{"x86 guest without dmi", probe("linux", nil, map[string]string{"/proc/cpuinfo": "processor\t: 0\nflags\t\t: fpu vme hypervisor lahf_lm\n"}), HostKindVM, "", "box"},
		{"bare metal", probe("linux", nil, map[string]string{"/sys/class/dmi/id/sys_vendor": "Dell Inc.\n", "/proc/cpuinfo": "flags\t\t: fpu vme\n"}), HostKindMetal, "", "box"},
		{"override", probe("linux", map[string]string{"SUPERAI_HOST_KIND": "VM", "SUPERAI_HOST_NAME": "orange1"}, nil), HostKindVM, "", "orange1"},
		{"bad override is ignored", probe("darwin", map[string]string{"SUPERAI_HOST_KIND": "toaster"}, nil), HostKindMac, "", "box"},
	}
	for _, c := range cases {
		h := detectHost(c.p)
		if h.Kind != c.kind || h.Parent != c.parent || h.Name != c.host {
			t.Errorf("%s: got %+v, want kind %q parent %q name %q", c.name, h, c.kind, c.parent, c.host)
		}
	}
}
