package domain

import "testing"

func TestIdHelpers(t *testing.T) {
	if !IsAppId("org1/ns1/web") || IsAppId("org1/ns1") || IsAppId("org1") {
		t.Error("IsAppId must accept only org/ns/app")
	}
	if org, ns, app, ok := SplitAppId("org1/ns1/web"); !ok || org != "org1" || ns != "ns1" || app != "web" {
		t.Errorf("SplitAppId = (%q,%q,%q,%v)", org, ns, app, ok)
	}
	if _, _, _, ok := SplitAppId("org1/ns1"); ok {
		t.Error("SplitAppId must reject a namespace id")
	}
	for id, want := range map[string]string{"org1/ns1/web": "org1", "org1/ns1": "org1", "org1": "org1"} {
		if got := OrgOf(id); got != want {
			t.Errorf("OrgOf(%q) = %q, want %q", id, got, want)
		}
	}
	// A namespace is its own namespace (a lender can be a namespace, not only an app).
	for id, want := range map[string]string{"org1/ns1/web": "org1/ns1", "org1/ns1": "org1/ns1", "org1": "org1"} {
		if got := NamespaceOf(id); got != want {
			t.Errorf("NamespaceOf(%q) = %q, want %q", id, got, want)
		}
	}
}
