package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/salesan/omnichannel/backend/internal/config"
)

// Draft generation.
//
// GPT writes a suggestion and nothing else. It never sends, never schedules, and
// never becomes the campaign body on its own — the composer puts the text in the
// editor and the operator has to read it and press the button. That is not
// caution for its own sake: a generated message goes to thousands of real
// customers under the workspace's own number, and a model that misreads the
// brief produces a mistake nobody can recall.
//
// The API key lives in the process environment and goes out in a header. It is
// never written to the database, never logged, and never reaches a response
// body — the log table has no column for it precisely so it cannot drift into
// one.

// ErrGPTUnavailable means no key is configured. The composer's other modes are
// unaffected, and the interface says so rather than failing at the last step.
var ErrGPTUnavailable = errors.New("campaign: generator GPT belum dikonfigurasi")

// DraftRequest is what the composer asked the model for.
type DraftRequest struct {
	// Brief is the operator's instruction, in their own words.
	Brief string
	// Tone and Language shape the result without needing a full prompt.
	Tone     string
	Language string
	// Variables are the placeholders the model may use, so it produces
	// <<nama>> rather than inventing a name.
	Variables []string
	// WithSpintax asks for alternatives in {a|b} form.
	WithSpintax bool
	// WithVariables lets the model turn the specifics of the caption — a name, a
	// promo code, a deadline — into placeholders of its own.
	//
	// Separate from Variables, which only lists what already exists. This is the
	// composer's "Variable" and "Keduanya" modes: the point of them is to find
	// the parts of a written caption that differ per recipient, which means
	// naming placeholders that are not in the list yet.
	WithVariables bool
}

// Draft modes, as the composer names them.
const (
	DraftModeSpintax  = "spintax"
	DraftModeVariable = "variable"
	DraftModeBoth     = "both"
)

// Draft is the model's answer plus what it cost.
type Draft struct {
	Text             string `json:"text"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

// GenerateDraft asks the model for one message.
func GenerateDraft(ctx context.Context, cfg *config.Config, req DraftRequest) (*Draft, error) {
	if !cfg.GPTEnabled() {
		return nil, ErrGPTUnavailable
	}
	if strings.TrimSpace(req.Brief) == "" {
		return nil, errors.New("campaign: instruksi untuk GPT masih kosong")
	}

	body, err := json.Marshal(map[string]any{
		"model": cfg.OpenAIModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt(req)},
			{"role": "user", "content": req.Brief},
		},
		"temperature": 0.8,
		"max_tokens":  600,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.OpenAIBaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.OpenAIAPIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("campaign: menghubungi penyedia GPT: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("campaign: jawaban GPT tidak dapat dibaca: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The provider's message is passed through, but never the request that
		// carried the key.
		detail := parsed.Error.Message
		if detail == "" {
			detail = resp.Status
		}
		return nil, fmt.Errorf("campaign: GPT menolak permintaan: %s", detail)
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("campaign: GPT tidak mengembalikan teks")
	}

	return &Draft{
		Text:             strings.TrimSpace(parsed.Choices[0].Message.Content),
		Model:            cfg.OpenAIModel,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
	}, nil
}

func systemPrompt(req DraftRequest) string {
	lang := req.Language
	if lang == "" {
		lang = "Bahasa Indonesia"
	}
	tone := req.Tone
	if tone == "" {
		tone = "ramah dan profesional"
	}

	var b strings.Builder
	b.WriteString("Kamu menulis satu pesan WhatsApp untuk campaign broadcast. ")
	fmt.Fprintf(&b, "Gunakan %s dengan nada %s. ", lang, tone)
	b.WriteString("Tulis hanya isi pesannya, tanpa penjelasan, tanpa tanda kutip, dan tanpa judul. ")
	b.WriteString("Jangan menjanjikan hal yang tidak disebutkan dalam instruksi. ")

	if len(req.Variables) > 0 {
		fmt.Fprintf(&b, "Variabel yang sudah tersedia: %s. Pakai apa adanya bila cocok. ",
			strings.Join(bracket(req.Variables), ", "))
	}

	if req.WithVariables {
		// The whole point of this mode is to find the parts of a written caption
		// that differ per recipient. Saying "no new variables" here would leave
		// the mode with nothing to do.
		b.WriteString("Ubah bagian pesan yang berbeda untuk tiap penerima menjadi variabel " +
			"dengan format <<nama_variabel>>. Nama variabel huruf kecil, tanpa spasi, " +
			"boleh memakai garis bawah. Maksimal lima variabel. " +
			"Jangan membuat variabel untuk kalimat utuh, hanya untuk potongan data " +
			"seperti nama, kode promo, produk, atau tanggal. ")
	} else if len(req.Variables) > 0 {
		b.WriteString("Jangan membuat variabel baru. ")
	}

	if req.WithSpintax {
		b.WriteString("Sediakan variasi kata dengan format spintax {pilihan1|pilihan2} " +
			"pada sapaan dan penutup saja, maksimal tiga kelompok. ")
		// Both syntaxes live in the same sentence, and a model that wraps a
		// variable in single braces turns a customer's name into a spintax
		// branch. Spelled out rather than left to be inferred.
		b.WriteString("Kurung kurawal tunggal hanya untuk spintax; variabel selalu " +
			"memakai kurung sudut ganda. ")
	}
	return b.String()
}

func bracket(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, "<<"+k+">>")
	}
	return out
}
