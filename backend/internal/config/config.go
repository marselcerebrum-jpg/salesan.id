// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every knob the server needs. Everything comes from the
// environment so the same binary runs locally and in Docker unchanged.
type Config struct {
	Env  string
	Port string

	// DatabaseURL is the Postgres DSN used by the application repositories.
	DatabaseURL string
	// WhatsmeowDatabaseURL is where whatsmeow keeps its own device/session
	// tables. Defaults to DatabaseURL; split it out if you prefer the WhatsApp
	// credentials to live in a separate database.
	WhatsmeowDatabaseURL string

	SupabaseURL       string
	SupabaseJWTSecret string
	SupabaseJWKSURL   string
	SupabaseAudience  string

	// SupabaseServiceRoleKey authenticates the server against Supabase Storage.
	// It bypasses RLS, so it stays on this side of the wire — never in a
	// response body, a log line, or anything the browser can reach.
	SupabaseServiceRoleKey string

	// StorageBucket is the *private* bucket media is written to.
	StorageBucket string
	// MediaURLTTL bounds how long a signed URL stays valid. Short enough that a
	// leaked link expires quickly, long enough that a video finishes playing.
	MediaURLTTL time.Duration

	// MediaDir is where media lands when Supabase Storage is not configured.
	// Not web-served: files leave it only through a signed, expiring link.
	MediaDir string
	// MediaSigningSecret signs those links. Derived from DATABASE_URL when
	// unset, so links survive a restart without another setting to remember.
	MediaSigningSecret string
	// PublicAPIURL is how a browser reaches this server. Signed local media
	// links are absolute, so they need it.
	PublicAPIURL string

	AllowedOrigins []string

	// WhatsApp behaviour
	QRTimeout          time.Duration
	ReconnectBaseDelay time.Duration
	ReconnectMaxDelay  time.Duration
	AutoMarkRead       bool

	// --- looking like a person rather than a script -------------------------
	//
	// WhatsApp watches for accounts that answer instantly, never show a typing
	// indicator, and fire messages back to back. An account that behaves that
	// way gets rate-limited and eventually banned — and the ban lands on the
	// customer's real business number, not on this software.
	//
	// HumanizeSending turns the whole sequence on: read the chat, appear
	// online, type for a while, pause, then send.
	HumanizeSending bool
	// PresenceOnline marks the account available while sending. WhatsApp only
	// delivers a typing indicator from a device it considers online, so
	// without this the typing is never seen. The cost is that contacts can see
	// the account as online, exactly as they would with a person at a phone.
	PresenceOnline bool
	// TypingMin/TypingMax bound how long the typing indicator shows, and
	// TypingCPS is the characters-per-second used to scale it with the length
	// of the message.
	TypingMin time.Duration
	TypingMax time.Duration
	TypingCPS int
	// SendMinGap is the least time between two messages from one account. It
	// is what stops a forward to twenty chats from going out as a burst.
	SendMinGap time.Duration

	// --- operational reporting ----------------------------------------------
	//
	// SLATargetSeconds is the response-time promise a personal chat is measured
	// against: answered within it, the cycle counts as met.
	SLATargetSeconds int
	// SLABusinessHours scores the cycle on time inside the admin's shift rather
	// than on wall-clock time. With it off, a message that arrives at 2am
	// consumes the target while nobody is on duty.
	SLABusinessHours bool
	// FollowUpGap is how long a chat must be quiet before an admin writing into
	// it counts as picking it back up rather than continuing.
	FollowUpGap time.Duration
	// MetricsReconcileInterval is how often the sweep repairs derived rows that
	// a lost event or a restart left behind.
	MetricsReconcileInterval time.Duration

	// --- Broadcast and WA Story ---------------------------------------------
	//
	// CampaignPollInterval is how often the scheduler asks the database what is
	// due. Short, because a campaign started from the interface should begin
	// promptly; the query is a single indexed lookup.
	CampaignPollInterval time.Duration
	// CampaignLease is how long a worker's claim on a campaign or a recipient
	// holds before another worker may take it. It has to outlast one send plus
	// its delay; too short and two workers overlap, too long and a crashed
	// backend blocks its own recovery.
	CampaignLease time.Duration
	// CampaignConcurrency is how many campaigns this process runs at once.
	// Devices inside one campaign each get their own goroutine regardless, so
	// this alone does not bound the load — CampaignSendConcurrency does.
	CampaignConcurrency int
	// CampaignSendConcurrency caps how many recipients are being sent to at the
	// same instant, across every campaign and every number in this process.
	//
	// Without it, "semua nomor" on an application holding twenty numbers starts
	// twenty senders, and four such campaigns start eighty — all writing through
	// a connection pool of eight that the HTTP API also needs. The queue would
	// not crash, but the interface in front of it would stop answering, which
	// from a desk looks exactly like the server going down.
	//
	// Each number still sends strictly in order and still waits out its own
	// delay profile; this only decides how many of them may be mid-send at once.
	CampaignSendConcurrency int
	// CampaignStoryConcurrency caps how many Story publications are being
	// pushed to WhatsApp at the same instant, across every campaign in this
	// process.
	//
	// A Story is not one send. WhatsApp addresses a status to every contact in
	// the number's address book, and whatsmeow encrypts the key for each of
	// their devices one by one: a number with twenty thousand saved contacts
	// takes ten to thirteen minutes of CPU per Story. Running the numbers of one
	// campaign one after another made a ten-number Story take two hours, while
	// running them all at once would pin every core and stall the inbox. Three
	// is the middle: a four-core server keeps one core for everything else.
	CampaignStoryConcurrency int
	// CampaignSendTimeout bounds one Broadcast send.
	//
	// whatsmeow waits for the server's acknowledgement with no deadline of its
	// own, so a send whose ack never comes waits forever. A number sends
	// strictly in order, so that one send freezes every recipient behind it,
	// the campaign never settles, and the recovery sweep never runs because the
	// campaign is still held by this process. Observed in production: a
	// broadcast to a forty-one member group stuck at "processing" with nothing
	// happening and nothing failing.
	//
	// An ordinary send finishes in seconds. Two minutes is not a target, it is
	// the point past which something is wrong.
	CampaignSendTimeout time.Duration
	// CampaignStoryTimeout bounds one Story publication the same way. Longer,
	// because a Story to twenty thousand contacts legitimately takes fifteen
	// minutes of encryption.
	CampaignStoryTimeout time.Duration
	// CampaignMediaMaxBytes caps a media download. Defaults to WhatsApp's own
	// video limit, which is the largest thing that could be sent anyway.
	CampaignMediaMaxBytes int64
	// CampaignMediaTimeout bounds the whole fetch, not just the handshake.
	CampaignMediaTimeout time.Duration
	// CampaignTempDir is where a fetched file lands before it is uploaded and
	// deleted. Empty uses the system temp directory.
	CampaignTempDir string

	// --- GPT draft generation ------------------------------------------------
	//
	// Optional. Without a key the composer's GPT mode reports itself unavailable
	// rather than failing obscurely, and every other mode keeps working.
	OpenAIAPIKey  string
	OpenAIBaseURL string
	OpenAIModel   string

	LogLevel string
}

// GPTEnabled reports whether draft generation can be offered.
func (c *Config) GPTEnabled() bool { return c.OpenAIAPIKey != "" }

// UseSupabaseStorage reports whether media should go to Supabase rather than
// to this machine's disk. It needs the service-role key; without one the local
// backend takes over so the feature still works.
func (c *Config) UseSupabaseStorage() bool {
	return c.SupabaseURL != "" && c.SupabaseServiceRoleKey != ""
}

// Load reads .env (if present) and then the process environment.
func Load() (*Config, error) {
	// .env is a developer convenience; its absence is not an error. A malformed
	// one, however, would silently strand every setting, so surface that.
	if err := LoadEnvFiles(".env"); err != nil {
		return nil, fmt.Errorf("read .env: %w", err)
	}

	cfg := &Config{
		Env:                  env("APP_ENV", "development"),
		Port:                 env("PORT", "8080"),
		DatabaseURL:          env("DATABASE_URL", ""),
		WhatsmeowDatabaseURL: env("WHATSMEOW_DATABASE_URL", ""),
		SupabaseURL:          strings.TrimRight(env("SUPABASE_URL", ""), "/"),
		SupabaseJWTSecret:    env("SUPABASE_JWT_SECRET", ""),
		SupabaseJWKSURL:      env("SUPABASE_JWKS_URL", ""),
		SupabaseAudience:     env("SUPABASE_JWT_AUDIENCE", "authenticated"),

		SupabaseServiceRoleKey: env("SUPABASE_SERVICE_ROLE_KEY", ""),
		StorageBucket:          env("SUPABASE_STORAGE_BUCKET", "wa-media"),
		MediaURLTTL:            duration("MEDIA_URL_TTL", time.Hour),
		MediaDir:               env("MEDIA_DIR", ".media"),
		MediaSigningSecret:     env("MEDIA_SIGNING_SECRET", ""),
		PublicAPIURL:           strings.TrimRight(env("PUBLIC_API_URL", "http://localhost:8080"), "/"),

		AllowedOrigins:     splitAndTrim(env("ALLOWED_ORIGINS", "http://localhost:3000")),
		QRTimeout:          duration("QR_TIMEOUT", 3*time.Minute),
		ReconnectBaseDelay: duration("RECONNECT_BASE_DELAY", 3*time.Second),
		ReconnectMaxDelay:  duration("RECONNECT_MAX_DELAY", 5*time.Minute),
		// Defaults on so read state is genuinely two-way: opening a chat here
		// clears it on the phone, just as reading it on the phone clears it
		// here. Set false to stay invisible (no blue ticks) at the cost of the
		// two sides drifting apart.
		AutoMarkRead: boolean("WA_AUTO_MARK_READ", true),

		HumanizeSending: boolean("WA_HUMANIZE", true),
		PresenceOnline:  boolean("WA_PRESENCE_ONLINE", true),
		TypingMin:       duration("WA_TYPING_MIN", 900*time.Millisecond),
		TypingMax:       duration("WA_TYPING_MAX", 4*time.Second),
		TypingCPS:       integer("WA_TYPING_CPS", 25),
		SendMinGap:      duration("WA_SEND_MIN_GAP", 1500*time.Millisecond),

		SLATargetSeconds: integer("SLA_TARGET_SECONDS", 15*60),
		// Off by default: a workspace with no rota entered yet would otherwise
		// have every cycle fall back to wall clock anyway, and turning it on
		// only once schedules exist keeps the meaning of the number stable.
		SLABusinessHours:         boolean("SLA_BUSINESS_HOURS", false),
		FollowUpGap:              duration("FOLLOWUP_GAP", 6*time.Hour),
		MetricsReconcileInterval: duration("METRICS_RECONCILE_INTERVAL", 15*time.Minute),

		CampaignPollInterval: duration("CAMPAIGN_POLL_INTERVAL", 5*time.Second),
		CampaignLease:        duration("CAMPAIGN_LEASE", 10*time.Minute),
		CampaignConcurrency:  integer("CAMPAIGN_CONCURRENCY", 4),
		// Six, against a repository pool of eight: a send is mostly waiting on
		// WhatsApp rather than on Postgres, so six in flight leaves the API room
		// to keep answering while a large broadcast runs.
		CampaignSendConcurrency:  integer("CAMPAIGN_SEND_CONCURRENCY", 6),
		CampaignStoryConcurrency: integer("CAMPAIGN_STORY_CONCURRENCY", 3),
		CampaignSendTimeout:      duration("CAMPAIGN_SEND_TIMEOUT", 2*time.Minute),
		CampaignStoryTimeout:     duration("CAMPAIGN_STORY_TIMEOUT", 45*time.Minute),
		CampaignMediaMaxBytes:    int64(integer("CAMPAIGN_MEDIA_MAX_BYTES", 64*1024*1024)),
		CampaignMediaTimeout:     duration("CAMPAIGN_MEDIA_TIMEOUT", 2*time.Minute),
		CampaignTempDir:          os.Getenv("CAMPAIGN_TEMP_DIR"),

		OpenAIAPIKey:  env("OPENAI_API_KEY", ""),
		OpenAIBaseURL: strings.TrimRight(env("OPENAI_BASE_URL", "https://api.openai.com"), "/"),
		OpenAIModel:   env("OPENAI_MODEL", "gpt-4o-mini"),

		LogLevel: env("LOG_LEVEL", "info"),
	}

	if cfg.WhatsmeowDatabaseURL == "" {
		cfg.WhatsmeowDatabaseURL = cfg.DatabaseURL
	}
	// Supabase publishes its JWKS at a well-known path; derive it so projects
	// using asymmetric signing keys work with only SUPABASE_URL set.
	if cfg.SupabaseJWKSURL == "" && cfg.SupabaseURL != "" {
		cfg.SupabaseJWKSURL = cfg.SupabaseURL + "/auth/v1/.well-known/jwks.json"
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.SupabaseJWTSecret == "" && c.SupabaseJWKSURL == "" {
		missing = append(missing, "SUPABASE_JWT_SECRET (or SUPABASE_URL / SUPABASE_JWKS_URL)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

// IsProduction reports whether the server should apply production defaults.
func (c *Config) IsProduction() bool { return c.Env == "production" }

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return fallback
}

func integer(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	return fallback
}

func boolean(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	return fallback
}

func splitAndTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
