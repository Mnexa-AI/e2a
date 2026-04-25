package outbound

import (
	"reflect"
	"testing"
)

func TestNormalizeAddrs(t *testing.T) {
	got, err := normalizeAddrs([]string{"Alice@Gmail.COM", " bob@test.com ", ""})
	if err != nil {
		t.Fatalf("normalizeAddrs: %v", err)
	}
	want := []string{"alice@gmail.com", "bob@test.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeAddrsDisplayName(t *testing.T) {
	got, err := normalizeAddrs([]string{"Alice <alice@GMAIL.com>"})
	if err != nil {
		t.Fatalf("normalizeAddrs: %v", err)
	}
	want := []string{"alice@gmail.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeAddrsInvalid(t *testing.T) {
	_, err := normalizeAddrs([]string{"not-an-email"})
	if err == nil {
		t.Error("expected error for invalid address")
	}
}

func TestDedupe(t *testing.T) {
	got := dedupe([]string{"a@b.com", "c@d.com", "A@B.com", "c@d.com"})
	want := []string{"a@b.com", "c@d.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRemoveAddrs(t *testing.T) {
	got := removeAddrs([]string{"a@b.com", "c@d.com", "e@f.com"}, []string{"c@d.com"})
	want := []string{"a@b.com", "e@f.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCrossFieldDedupe(t *testing.T) {
	// Simulate To > CC > BCC priority
	to := []string{"alice@test.com", "bob@test.com"}
	cc := []string{"bob@test.com", "carol@test.com"}
	bcc := []string{"carol@test.com", "dave@test.com"}

	cc = removeAddrs(cc, to)
	bcc = removeAddrs(bcc, to)
	bcc = removeAddrs(bcc, cc)

	wantCC := []string{"carol@test.com"}
	wantBCC := []string{"dave@test.com"}

	if !reflect.DeepEqual(cc, wantCC) {
		t.Errorf("cc = %v, want %v", cc, wantCC)
	}
	if !reflect.DeepEqual(bcc, wantBCC) {
		t.Errorf("bcc = %v, want %v", bcc, wantBCC)
	}
}
