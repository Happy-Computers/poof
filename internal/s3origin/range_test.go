package s3origin

import "testing"

func TestParseBytesRange(t *testing.T) {
	r1, err := ParseBytesRange("bytes=0-99", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Start != 0 || r1.EndInclusive != 99 {
		t.Fatalf("got %+v", r1)
	}

	r2, err := ParseBytesRange("bytes=10-", 100)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Start != 10 || r2.EndInclusive != 99 {
		t.Fatalf("got %+v", r2)
	}

	big, err := ParseBytesRange("bytes=0-", 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if big.Start != 0 || big.EndInclusive != MaxRangeBytes-1 {
		t.Fatalf("got %+v", big)
	}

	r3, err := ParseBytesRange("bytes=-20", 100)
	if err != nil {
		t.Fatal(err)
	}
	if r3.Start != 80 || r3.EndInclusive != 99 {
		t.Fatalf("got %+v", r3)
	}

	if _, err := ParseBytesRange("bytes=0-10,11-20", 100); err == nil {
		t.Fatal("expected error for multi-range")
	}
	if _, err := ParseBytesRange("bytes=100-200", 100); err == nil {
		t.Fatal("expected error for out-of-bounds start")
	}
}
