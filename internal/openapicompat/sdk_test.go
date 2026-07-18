package openapicompat

import (
	"strings"
	"testing"
)

const sdkSurfaceBase = `
openapi: 3.1.0
paths:
  /stable:
    get:
      operationId: getStable
      tags: [things, reads]
      responses: {}
  /beta:
    get:
      operationId: getBeta
      tags: [beta]
      x-stability-level: beta
      responses: {}
components:
  schemas:
    StableView:
      type: object
    BetaView:
      type: object
      x-stability-level: beta
`

func TestCheckStableSDKSurfaceAcceptsEquivalentDocuments(t *testing.T) {
	if err := CheckStableSDKSurface(strings.NewReader(sdkSurfaceBase), strings.NewReader(sdkSurfaceBase)); err != nil {
		t.Fatalf("CheckStableSDKSurface: %v", err)
	}
}

func TestCheckStableSDKSurfaceRejectsStableSchemaRename(t *testing.T) {
	revision := strings.Replace(sdkSurfaceBase, "StableView:", "RenamedStableView:", 1)
	err := CheckStableSDKSurface(strings.NewReader(sdkSurfaceBase), strings.NewReader(revision))
	if err == nil || !strings.Contains(err.Error(), "stable-sdk-schema-removed") {
		t.Fatalf("error = %v, want stable-sdk-schema-removed", err)
	}
}

func TestCheckStableSDKSurfaceRejectsStableSchemaDemotion(t *testing.T) {
	revision := strings.Replace(sdkSurfaceBase, "StableView:\n      type: object", "StableView:\n      type: object\n      x-stability-level: beta", 1)
	err := CheckStableSDKSurface(strings.NewReader(sdkSurfaceBase), strings.NewReader(revision))
	if err == nil || !strings.Contains(err.Error(), "stable-sdk-schema-stability-decreased") {
		t.Fatalf("error = %v, want stable-sdk-schema-stability-decreased", err)
	}
}

func TestCheckStableSDKSurfaceAllowsBetaSchemaRename(t *testing.T) {
	revision := strings.Replace(sdkSurfaceBase, "BetaView:", "RenamedBetaView:", 1)
	if err := CheckStableSDKSurface(strings.NewReader(sdkSurfaceBase), strings.NewReader(revision)); err != nil {
		t.Fatalf("CheckStableSDKSurface: %v", err)
	}
}

func TestCheckStableSDKSurfaceRejectsStableOperationTagChange(t *testing.T) {
	revision := strings.Replace(sdkSurfaceBase, "tags: [things, reads]", "tags: [reads, things]", 1)
	err := CheckStableSDKSurface(strings.NewReader(sdkSurfaceBase), strings.NewReader(revision))
	if err == nil || !strings.Contains(err.Error(), "stable-sdk-operation-tags-changed") {
		t.Fatalf("error = %v, want stable-sdk-operation-tags-changed", err)
	}
}

func TestCheckStableSDKSurfaceRejectsTagChangeThroughPathItemRef(t *testing.T) {
	revision := `
openapi: 3.1.0
paths:
  /stable:
    $ref: "#/components/pathItems/Stable"
  /beta:
    get:
      operationId: getBeta
      tags: [beta]
      x-stability-level: beta
      responses: {}
components:
  pathItems:
    Stable:
      get:
        operationId: getStable
        tags: [renamed]
        responses: {}
  schemas:
    StableView:
      type: object
    BetaView:
      type: object
      x-stability-level: beta
`
	err := CheckStableSDKSurface(strings.NewReader(sdkSurfaceBase), strings.NewReader(revision))
	if err == nil || !strings.Contains(err.Error(), "stable-sdk-operation-tags-changed") {
		t.Fatalf("error = %v, want stable-sdk-operation-tags-changed", err)
	}
}

func TestCheckStableSDKSurfaceAllowsBetaOperationTagChange(t *testing.T) {
	revision := strings.Replace(sdkSurfaceBase, "tags: [beta]", "tags: [experimental]", 1)
	if err := CheckStableSDKSurface(strings.NewReader(sdkSurfaceBase), strings.NewReader(revision)); err != nil {
		t.Fatalf("CheckStableSDKSurface: %v", err)
	}
}
