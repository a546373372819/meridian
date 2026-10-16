package appref

import "testing"

func TestRoundTrip(t *testing.T) {
	name := ContainerName("org1", "ns1", "web")
	if name != "org1_ns1_web" {
		t.Fatalf("ContainerName = %q, want org1_ns1_web", name)
	}
	if got := AppID(name); got != "org1/ns1/web" {
		t.Errorf("AppID(%q) = %q, want org1/ns1/web", name, got)
	}
}

func TestAppNameMayContainSeparator(t *testing.T) {
	org, ns, app, ok := Parse("org1_ns1_my_app")
	if !ok || org != "org1" || ns != "ns1" || app != "my_app" {
		t.Errorf("Parse = (%q,%q,%q,%v), want org1/ns1/my_app", org, ns, app, ok)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"web", "org1_ns1", "", "_ns_app", "org__app"} {
		if id := AppID(bad); id != "" {
			t.Errorf("AppID(%q) = %q, want empty (malformed)", bad, id)
		}
	}
}
