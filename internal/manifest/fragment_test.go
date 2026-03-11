package manifest

import "testing"

func TestParseAndSelectArtifact(t *testing.T) {
	t.Parallel()

	fragment, err := ParseFragment([]byte(`{
	  "schemaVersion":"receiver-manifest-fragment/v1",
	  "receiverVersion":"v1.1.0",
	  "channel":"stable",
	  "artifacts":[
	    {"platform":"raspberry_pi","arch":"arm64","kind":"systemd_layout","filename":"a.tar.gz","relativeUrl":"receiver/stable/v1.1.0/a.tar.gz","checksumSha256":"abc","sizeBytes":1,"recommended":true},
	    {"platform":"linux","arch":"amd64","kind":"binary","filename":"b.tar.gz","relativeUrl":"receiver/stable/v1.1.0/b.tar.gz","checksumSha256":"def","sizeBytes":2}
	  ]
	}`))
	if err != nil {
		t.Fatalf("ParseFragment returned error: %v", err)
	}

	artifact, ok := SelectArtifact(fragment, "raspberry_pi", "arm64", "systemd_layout")
	if !ok {
		t.Fatal("expected artifact selection")
	}
	if artifact.Filename != "a.tar.gz" {
		t.Fatalf("unexpected artifact filename: %s", artifact.Filename)
	}
}
