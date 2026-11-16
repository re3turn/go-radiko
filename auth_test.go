package radiko

import (
	"context"
	"testing"
)

func TestAuthorizeToken(t *testing.T) {
	if isOutsideJP() {
		t.Skip("Skipping test in limited mode.")
	}

	c, err := New("")
	if err != nil {
		t.Fatalf("Failed to construct client: %s", err)
	}

	ctx := context.Background()
	authToken, err := c.AuthorizeToken(ctx)
	if err != nil {
		t.Error(err)
	}
	if len(authToken) == 0 {
		t.Error("AuthToken is empty.")
	}
}

func TestVerifyAuth2Response(t *testing.T) {
	cases := []struct {
		slc         []string
		expectedErr bool
	}{
		{
			slc: []string{`

	JP13`, "東京都", "tokyo", "Japan",
			},
			expectedErr: false,
		},
		{
			slc:         []string{},
			expectedErr: true,
		},
		{
			slc:         []string{"OUT"},
			expectedErr: true,
		},
	}
	for _, c := range cases {
		err := verifyAuth2Response(c.slc)
		if c.expectedErr {
			if err == nil {
				t.Error("Should detect an error.")
			}
			continue
		}
		if err != nil {
			t.Error(err)
		}
	}
}
