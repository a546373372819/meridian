// converts meridia's per app resource quotas into tugs Patch
package appquota

import "github.com/c12s/tug/pkg/resource"

// BytesPerMiB converts meridians mem quota (MiB) to bytes
const BytesPerMiB = 1024 * 1024

// converts quotas such as {"cpu": 2, "mem": 512} into a tug Patch that sets those limits. Keys other than cpu and mem are ignored
func ToPatch(quotas map[string]float64) resource.Patch {
	var p resource.Patch
	if v, ok := quotas["cpu"]; ok {
		cpu := v
		p.CPU = &cpu
	}
	if v, ok := quotas["mem"]; ok {
		bytes := int64(v * BytesPerMiB)
		p.Memory = &bytes
	}
	return p
}
