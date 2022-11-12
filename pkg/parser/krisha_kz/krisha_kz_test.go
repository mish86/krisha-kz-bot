package krishakz_test

import (
	"encoding/json"
	"fmt"
	"io"
	"krisha_kz_bot/pkg/holder"
	"krisha_kz_bot/pkg/parser"
	krishakz "krisha_kz_bot/pkg/parser/krisha_kz"
	"os"
	"testing"
	"time"
)

type testUnit struct {
	testData      func(string) io.ReadCloser
	getNow        func(*time.Location) time.Time
	getLoc        func() *time.Location
	getMockParser func(func() time.Time) parser.Parser[holder.WithDT[string]]
}

func newTestUnit(t *testing.T, now string, locName string) *testUnit {
	return &testUnit{
		testData: func(testPage string) io.ReadCloser {
			f, err := os.Open(testPage)
			if err != nil {
				t.Fatalf("failed to read test data, got error %v", err)
			}
			return f
		},
		getNow: func(loc *time.Location) time.Time {
			now, err := time.ParseInLocation("02 Jan 06 15:04", now, loc)
			if err != nil {
				t.Fatalf("failed to parse time, got error %v", err)
			}
			return now
		},
		getLoc: func() *time.Location {
			loc, err := time.LoadLocation(locName)
			if err != nil {
				t.Fatalf("failed to parse location, got error %v", err)
			}
			return loc
		},
		getMockParser: func(fncNow func() time.Time) parser.Parser[holder.WithDT[string]] {
			return &krishakz.Parser{
				GetNow: fncNow,
			}
		},
	}
}

func TestParser(t *testing.T) {
	const testFile = "./test_data/test_data.json"

	var cases []struct {
		testUnit
		TestPage string   `json:"TestPage"`
		Now      string   `json:"Now"`
		LocName  string   `json:"LocName"`
		Want     []string `json:"Want"`
	}

	if data, err1 := os.ReadFile(testFile); err1 != nil {
		t.Fatalf("failed to read file %s, got error %v", testFile, err1)
	} else if err2 := json.Unmarshal(data, &cases); err2 != nil {
		t.Fatalf("failed to unmarshal test cases from %s, got error %v", testFile, err2)
	}

	for i, c := range cases {
		t.Run(fmt.Sprintf("Test case %d with test data %s", i, c.TestPage), func(t *testing.T) {
			c.testUnit = *newTestUnit(t, c.Now, c.LocName)

			mockParser := c.getMockParser(func() time.Time {
				return c.getNow(c.getLoc())
			})

			er := make([]string, len(c.Want))
			copy(er, c.Want)

			f := c.testData(c.TestPage)
			defer f.Close()

			if err := mockParser.Parse(f, func(val holder.WithDT[string]) {
				got := val.GetValue()
				found := false
				for i, want := range er {
					if want == got {
						found = true
						er = append(er[:i], er[i+1:]...)
						break
					}
				}
				if !found {
					t.Errorf("unexpected parser result of %s in case %d, want %v, got error %v", c.TestPage, i, c.Want, got)
				}
			}); err != nil {
				t.Errorf("failed to parse %s in case %d, got error %v", c.TestPage, i, err)
			}

			if len(er) != 0 {
				t.Errorf("not all results found on %s in case %d, want %v", c.TestPage, i, er)
			}
		})
	}
}
