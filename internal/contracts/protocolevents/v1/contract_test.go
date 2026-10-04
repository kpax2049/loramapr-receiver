package v1_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/ucarion/jcs"
)

type goldenManifest struct {
	Valid   []string `json:"valid"`
	Invalid []string `json:"invalid"`
	JCS     []string `json:"jcs"`
}

type bundleManifest struct {
	SchemaSHA256 string            `json:"schemaSha256"`
	BundleSHA256 string            `json:"bundleSha256"`
	Files        map[string]string `json:"files"`
}

type upstreamManifest struct {
	CloudRevision string `json:"cloudRevision"`
	SchemaSHA256  string `json:"schemaSha256"`
	BundleSHA256  string `json:"bundleSha256"`
}

func TestVendoredNormalizedEventContract(t *testing.T) {
	t.Parallel()

	root := packageRoot(t)
	bundle := readJSON[bundleManifest](t, filepath.Join(root, "bundle.json"))
	upstream := readJSON[upstreamManifest](t, filepath.Join(root, "UPSTREAM.json"))
	if upstream.CloudRevision == "" {
		t.Fatal("UPSTREAM.json must identify the cloud contract revision")
	}
	if upstream.SchemaSHA256 != bundle.SchemaSHA256 || upstream.BundleSHA256 != bundle.BundleSHA256 {
		t.Fatal("UPSTREAM.json digests do not match the vendored bundle")
	}

	paths := make([]string, 0, len(bundle.Files))
	for path := range bundle.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var bundleInput bytes.Buffer
	for _, relativePath := range paths {
		contents := readFile(t, filepath.Join(root, filepath.FromSlash(relativePath)))
		actual := sha256Hex(contents)
		if actual != bundle.Files[relativePath] {
			t.Fatalf("%s digest = %s, want %s", relativePath, actual, bundle.Files[relativePath])
		}
		fmt.Fprintf(&bundleInput, "%s\x00%s\n", relativePath, actual)
	}
	if actual := sha256Hex(bundleInput.Bytes()); actual != bundle.BundleSHA256 {
		t.Fatalf("bundle digest = %s, want %s", actual, bundle.BundleSHA256)
	}
	if bundle.Files["normalized-event.schema.json"] != bundle.SchemaSHA256 {
		t.Fatal("schema digest does not match the bundle entry")
	}

	schemaDocument, err := jsonschema.UnmarshalJSON(
		bytes.NewReader(readFile(t, filepath.Join(root, "normalized-event.schema.json"))),
	)
	if err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const schemaURL = "https://loramapr.dev/contracts/protocol-events/v1/normalized-event.schema.json"
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	goldenRoot := filepath.Join(root, "golden")
	manifest := readJSON[goldenManifest](t, filepath.Join(goldenRoot, "manifest.json"))
	for _, relativePath := range manifest.Valid {
		relativePath := relativePath
		t.Run("valid/"+relativePath, func(t *testing.T) {
			instance, err := jsonschema.UnmarshalJSON(
				bytes.NewReader(readFile(t, filepath.Join(goldenRoot, filepath.FromSlash(relativePath)))),
			)
			if err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Fatalf("valid fixture rejected: %v", err)
			}
		})
	}
	for _, relativePath := range manifest.Invalid {
		relativePath := relativePath
		t.Run("invalid/"+relativePath, func(t *testing.T) {
			instance, err := jsonschema.UnmarshalJSON(
				bytes.NewReader(readFile(t, filepath.Join(goldenRoot, filepath.FromSlash(relativePath)))),
			)
			if err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			if err := schema.Validate(instance); err == nil {
				t.Fatal("invalid fixture unexpectedly passed")
			}
		})
	}
	for _, relativePath := range manifest.JCS {
		relativePath := relativePath
		t.Run("jcs/"+relativePath, func(t *testing.T) {
			fixture := readJSON[struct {
				Input     any    `json:"input"`
				Canonical string `json:"canonical"`
			}](t, filepath.Join(goldenRoot, filepath.FromSlash(relativePath)))
			actual, err := jcs.Format(fixture.Input)
			if err != nil {
				t.Fatalf("canonicalize: %v", err)
			}
			if actual != fixture.Canonical {
				t.Fatalf("canonical JSON = %q, want %q", actual, fixture.Canonical)
			}
		})
	}
}

func packageRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("get package directory: %v", err)
	}
	return root
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(readFile(t, path), &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
