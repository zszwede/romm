package httpapi

import (
	"reflect"
	"testing"
)

func TestSortByHostOrder(t *testing.T) {
	order := []string{"1fichier.com", "archive.org"}
	// archive.org primary, 1fichier mirror -> 1fichier must come first.
	urls := []string{"https://archive.org/download/x", "https://1fichier.com/?abc"}
	sortByHostOrder(urls, order)
	want := []string{"https://1fichier.com/?abc", "https://archive.org/download/x"}
	if !reflect.DeepEqual(urls, want) {
		t.Errorf("got %v, want %v", urls, want)
	}
	// An unlisted host sorts last, stably.
	urls2 := []string{"https://other.com/a", "https://archive.org/b", "https://1fichier.com/c"}
	sortByHostOrder(urls2, order)
	if urls2[0] != "https://1fichier.com/c" || urls2[2] != "https://other.com/a" {
		t.Errorf("unlisted-last failed: %v", urls2)
	}
}
