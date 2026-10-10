package callertoken_test

import (
	"strings"
	"testing"

	"github.com/codefly-dev/core/callertoken"
)

var warehouse = callertoken.Listener{
	TokenVar:     "SWH_AUTH_TOKEN",
	AnonymousVar: "SWH_ALLOW_ANONYMOUS",
	Exposure:     "can run SQL against the bound database",
}

func TestCheckListener(t *testing.T) {
	for _, tc := range []struct {
		name      string
		token     string
		anonymous bool
		wantErr   []string // substrings; nil means accepted
	}{
		{"token alone", "s3cret", false, nil},
		{"anonymous opt-out alone", "", true, nil},
		{"both is a contradiction", "s3cret", true, []string{
			"SWH_AUTH_TOKEN and SWH_ALLOW_ANONYMOUS=true are mutually exclusive",
		}},
		{"neither", "", false, []string{
			"SWH_AUTH_TOKEN is required",
			"can run SQL against the bound database",
			"SWH_ALLOW_ANONYMOUS=true",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := callertoken.CheckListener(tc.token, tc.anonymous, warehouse)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted a configuration it must refuse")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "%!") {
				t.Errorf("error has a formatting defect: %q", err)
			}
		})
	}
}

func TestCheckListenerNeverEchoesTheToken(t *testing.T) {
	err := callertoken.CheckListener("s3cret-value", true, warehouse)
	if err == nil || strings.Contains(err.Error(), "s3cret-value") {
		t.Fatalf("err = %v", err)
	}
}
