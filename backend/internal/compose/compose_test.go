package compose

import (
	"math/rand"
	"strings"
	"testing"
)

// fixed returns a deterministic source, so a test asserts on behaviour rather
// than on luck.
func fixed(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

func TestSpinPicksOneBranch(t *testing.T) {
	const tmpl = "{Halo|Hai|Selamat pagi} Kak"
	allowed := map[string]bool{"Halo Kak": true, "Hai Kak": true, "Selamat pagi Kak": true}

	seen := map[string]bool{}
	for seed := int64(0); seed < 60; seed++ {
		got, err := Spin(tmpl, fixed(seed))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !allowed[got] {
			t.Fatalf("seed %d produced %q, which is not one of the branches", seed, got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatalf("spintax never varied across 60 seeds, only saw %v", seen)
	}
}

func TestSpinLeavesVariablesAlone(t *testing.T) {
	// The pipe inside {{...}} is a fallback, not a spintax separator. Getting
	// this wrong would let a contact whose name contains a brace change the
	// message that reaches them.
	const tmpl = "{Halo|Hai} {{nama|Kak}}, apa kabar?"
	for seed := int64(0); seed < 30; seed++ {
		got, err := Spin(tmpl, fixed(seed))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !strings.Contains(got, "{{nama|Kak}}") {
			t.Fatalf("seed %d: variable was consumed by the spinner: %q", seed, got)
		}
	}
}

func TestSpinNested(t *testing.T) {
	const tmpl = "{Selamat {pagi|siang}|Halo}"
	allowed := map[string]bool{"Selamat pagi": true, "Selamat siang": true, "Halo": true}
	for seed := int64(0); seed < 40; seed++ {
		got, err := Spin(tmpl, fixed(seed))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !allowed[got] {
			t.Fatalf("seed %d produced %q", seed, got)
		}
	}
}

func TestSpinRefusesRunawayNesting(t *testing.T) {
	tmpl := strings.Repeat("{a|", MaxDepth+2) + "b" + strings.Repeat("}", MaxDepth+2)
	if _, err := Spin(tmpl, fixed(1)); err == nil {
		t.Fatal("expected nesting past MaxDepth to be refused")
	}
}

func TestSpinTreatsUnbalancedBracesAsText(t *testing.T) {
	// "diskon 50% { hari ini" is a typo, not a template. Refusing to send would
	// be a worse answer than sending what was written.
	const tmpl = "diskon 50% { hari ini"
	got, err := Spin(tmpl, fixed(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "hari ini") {
		t.Fatalf("text was lost: %q", got)
	}
}

func TestRenderSubstitutesAndFallsBack(t *testing.T) {
	cases := []struct {
		name     string
		tmpl     string
		vars     map[string]string
		want     string
		wantMiss []string
	}{
		{
			name: "value present",
			tmpl: "Halo {{nama}}",
			vars: map[string]string{"nama": "Budi"},
			want: "Halo Budi",
		},
		{
			name: "fallback used when empty",
			tmpl: "Halo {{nama|Kak}}",
			vars: map[string]string{},
			want: "Halo Kak",
		},
		{
			name: "fallback used when value is blank",
			tmpl: "Halo {{nama|Kak}}",
			vars: map[string]string{"nama": ""},
			want: "Halo Kak",
		},
		{
			name:     "no value and no fallback is reported",
			tmpl:     "Kode: {{kode_promo}}",
			vars:     map[string]string{},
			want:     "Kode: {{kode_promo}}",
			wantMiss: []string{"kode_promo"},
		},
		{
			name: "case and spacing are normalised",
			tmpl: "Halo {{ NAMA }}",
			vars: map[string]string{"nama": "Budi"},
			want: "Halo Budi",
		},
		{
			name: "non-identifiers are left alone",
			tmpl: "hitung {{2+2}}",
			vars: map[string]string{},
			want: "hitung {{2+2}}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, missing := Render(tc.tmpl, tc.vars)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if len(missing) != len(tc.wantMiss) {
				t.Fatalf("missing = %v, want %v", missing, tc.wantMiss)
			}
			for i := range missing {
				if missing[i] != tc.wantMiss[i] {
					t.Errorf("missing[%d] = %q, want %q", i, missing[i], tc.wantMiss[i])
				}
			}
		})
	}
}

func TestRenderNeverBlanksAMissingVariable(t *testing.T) {
	// "Halo ," in front of a customer is what silently substituting an empty
	// string produces. The placeholder is left visible instead, and the caller
	// is told, so the review screen can refuse to send.
	got, missing := Render("Halo {{nama}}, terima kasih", nil)
	if strings.Contains(got, "Halo , ") {
		t.Fatalf("missing variable was blanked out: %q", got)
	}
	if len(missing) != 1 || missing[0] != "nama" {
		t.Fatalf("missing = %v, want [nama]", missing)
	}
}

func TestBuildSpinsThenRenders(t *testing.T) {
	msg, err := Build("{Halo|Hai} {{nama|Kak}}", map[string]string{"nama": "Sari"}, fixed(3))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(msg.Body, "Sari") {
		t.Fatalf("variable was not filled after spinning: %q", msg.Body)
	}
	if strings.ContainsAny(msg.Body, "{}") {
		t.Fatalf("template syntax survived into the message: %q", msg.Body)
	}
	if len(msg.Missing) != 0 {
		t.Fatalf("unexpected missing variables: %v", msg.Missing)
	}
}

func TestPlaceholders(t *testing.T) {
	got := Placeholders("Halo {{nama|Kak}}, kode {{kode_promo}} untuk {{nama}}")
	want := []string{"nama", "kode_promo"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	if problems := Validate("{Halo|Hai} {{nama|Kak}}"); len(problems) != 0 {
		t.Fatalf("a valid template was rejected: %v", problems)
	}
	if problems := Validate("{Halo|} Kak"); len(problems) == 0 {
		t.Fatal("an empty spintax branch should be reported: it sends a message with a word missing")
	}
	if problems := Validate("{Halo|Hai Kak"); len(problems) == 0 {
		t.Fatal("unbalanced braces should be reported")
	}
}

// The composer writes variables as <<nama>> because braces already mean spintax
// on the same screen. Both spellings have to render, and a template mixing the
// two must not let a recipient's own name be mistaken for a spintax branch.
func TestAngleBracketVariables(t *testing.T) {
	rng := rand.New(rand.NewSource(7))

	got, err := Build("Halo <<nama>>", map[string]string{"nama": "Budi"}, rng)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got.Body != "Halo Budi" {
		t.Fatalf("angle variable not substituted: %q", got.Body)
	}

	got, err = Build("Halo <<nama|Kak>>", map[string]string{}, rng)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got.Body != "Halo Kak" {
		t.Fatalf("angle fallback not used: %q", got.Body)
	}

	// A name carrying a pipe must survive as text. If variables were substituted
	// before spinning, "Budi|Ani" would become a spintax group and half the name
	// would reach the customer.
	got, err = Build("{Halo|Hai} <<nama>>", map[string]string{"nama": "Budi|Ani"}, rng)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.HasSuffix(got.Body, "Budi|Ani") {
		t.Fatalf("a piped value was re-spun: %q", got.Body)
	}

	if names := Placeholders("Halo <<nama>>, kode <<kode_promo>>"); len(names) != 2 ||
		names[0] != "nama" || names[1] != "kode_promo" {
		t.Fatalf("angle placeholders not listed: %v", names)
	}

	// The older spelling still has to work: campaigns are stored with it.
	got, err = Build("Halo {{nama}}", map[string]string{"nama": "Budi"}, rng)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got.Body != "Halo Budi" {
		t.Fatalf("brace variable stopped working: %q", got.Body)
	}

	if problems := Validate("{Halo|Hai} <<nama|Kak>>"); len(problems) != 0 {
		t.Fatalf("a valid angle template was rejected: %v", problems)
	}

	// Angle brackets that are not a variable are text, not syntax.
	got, err = Build("harga << 100 ribu", map[string]string{}, rng)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got.Body != "harga << 100 ribu" {
		t.Fatalf("stray angle brackets were rewritten: %q", got.Body)
	}
}
