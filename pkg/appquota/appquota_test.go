package appquota

import "testing"

func TestToPatch(t *testing.T) {
	p := ToPatch(map[string]float64{"cpu": 1.5, "mem": 512, "disk": 1024})
	if p.CPU == nil || *p.CPU != 1.5 {
		t.Errorf("cpu = %v, want 1.5", p.CPU)
	}
	if p.Memory == nil || *p.Memory != 512*BytesPerMiB {
		t.Errorf("memory = %v, want %d bytes (512 MiB)", p.Memory, 512*BytesPerMiB)
	}
	if p.MemorySwap != nil || p.MemoryReservation != nil || p.IOWeight != nil {
		t.Errorf("only cpu and mem may be set, got %+v", p)
	}
	if !ToPatch(map[string]float64{"disk": 10}).IsEmpty() {
		t.Error("disk-only quotas must give an empty patch")
	}
}
