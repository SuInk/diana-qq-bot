package llm

import "testing"

func TestGeminiOutputTokenLimit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		value   int64
		want    int32
		wantErr bool
	}{
		{name: "zero", value: 0, want: 0},
		{name: "positive", value: 4096, want: 4096},
		{name: "maximum", value: maxGeminiOutputTokens, want: int32(maxGeminiOutputTokens)},
		{name: "negative", value: -1, wantErr: true},
		{name: "overflow", value: maxGeminiOutputTokens + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := geminiOutputTokenLimit(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("geminiOutputTokenLimit(%d) error = %v, wantErr %v", test.value, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("geminiOutputTokenLimit(%d) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}
