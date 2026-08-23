package s3origin

import (
	"testing"

	"github.com/amaan/infinity-storage/internal/catalog"
)

func TestNormalizeListPrefix(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"account/project":  "account/project/",
		"account/project/": "account/project/",
	}
	for input, expected := range cases {
		if actual := normalizeListPrefix(input); actual != expected {
			t.Fatalf("normalizeListPrefix(%q)=%q", input, actual)
		}
	}
}

func TestNewStoreRejectsEmptyAndDup(t *testing.T) {
	_, err := newStore(nil, "b", "", []ObjectMeta{{Name: "a", Key: "a", Size: 0}})
	if err == nil {
		t.Fatal("empty size")
	}
	_, err = newStore(nil, "b", "", []ObjectMeta{
		{Name: "a", Key: "a", Size: 1},
		{Name: "a", Key: "a2", Size: 2},
	})
	if err == nil {
		t.Fatal("dup")
	}
}

func TestNewStoreMaxFiles(t *testing.T) {
	metas := make([]ObjectMeta, catalog.MaxFiles+1)
	for i := range metas {
		name := itoa(i) + ".bin"
		metas[i] = ObjectMeta{Name: name, Key: name, Size: 1}
	}
	_, err := newStore(nil, "b", "", metas)
	if err == nil {
		t.Fatal("want too many")
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = digits[i%10]
		i /= 10
	}
	return string(b[pos:])
}
