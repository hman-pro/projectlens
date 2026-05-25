package projects

import "testing"

func TestValidateSlug(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"ingest", true},
		{"projectlens", true},
		{"a_b-c-9", true},
		{"", false},
		{"Ingest", false},
		{"ingest!", false},
		{"-leading", false},
	}
	for _, c := range cases {
		err := ValidateSlug(c.in)
		if (err == nil) != c.ok {
			t.Errorf("ValidateSlug(%q) ok=%v err=%v", c.in, c.ok, err)
		}
	}
}

func TestValidateStorageSchema(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"ingest", true},
		{"projectlens", true},
		{"a1_2", true},
		{"", false},
		{"public", false},
		{"pg_anything", false},
		{"1leading", false},
		{"with-dash", false},
		{"With_Upper", false},
	}
	for _, c := range cases {
		err := ValidateStorageSchema(c.in)
		if (err == nil) != c.ok {
			t.Errorf("ValidateStorageSchema(%q) ok=%v err=%v", c.in, c.ok, err)
		}
	}
}
