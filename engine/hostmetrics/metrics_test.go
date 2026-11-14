package hostmetrics

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestRealHostSample(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("unsupported OS")
	}
	var sampler Sampler
	first := sampler.Sample()
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if first.CPU != nil {
		t.Fatal("first sample invents a CPU percentage")
	}
	time.Sleep(100 * time.Millisecond)
	sample := sampler.Sample()
	if err := sample.Validate(); err != nil {
		t.Fatal(err)
	}
	if sample.CPU == nil || sample.Memory == nil || len(sample.Disks) == 0 || sample.Uptime == nil {
		t.Fatalf("missing real measurements: %+v", sample)
	}
	if runtime.GOOS == "linux" {
		for _, disk := range sample.Disks {
			info, err := os.Stat(disk.Name)
			if err != nil || !info.IsDir() {
				t.Fatalf("file bind mount shown as disk: %s", disk.Name)
			}
		}
	}
	t.Logf("OS=%s arch=%s CPU=%s cores=%d use=%.3f memory=%+v disk=%+v uptime=%d", sample.OSVersion, sample.Arch, sample.CPUModel, sample.CPUs, *sample.CPU, *sample.Memory, sample.Disks, *sample.Uptime)
}
