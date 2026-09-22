package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/salesan/omnichannel/backend/internal/auth"
	"github.com/salesan/omnichannel/backend/internal/campaign"
	"github.com/salesan/omnichannel/backend/internal/config"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// Server wires the HTTP layer to its collaborators.
type Server struct {
	cfg      *config.Config
	repo     *repository.Repo
	manager  *wa.Manager
	hub      *realtime.Hub
	verifier *auth.Verifier
	// admin creates Supabase accounts for new team members. Usable only when a
	// service-role key is configured; without one it reports itself unavailable
	// rather than failing obscurely.
	admin *auth.Admin
	// runner is the Broadcast/Story scheduler. The API never sends anything
	// itself: it writes the campaign and nudges the queue, so an HTTP timeout
	// can never leave a half-sent campaign behind.
	runner *campaign.Runner
	// resolver turns a target selection into recipients, and is shared by the
	// review endpoint and the create endpoint so the two cannot disagree.
	resolver *campaign.Resolver
	log      *slog.Logger
}

// NewServer builds the API server.
func NewServer(
	cfg *config.Config,
	repo *repository.Repo,
	manager *wa.Manager,
	hub *realtime.Hub,
	verifier *auth.Verifier,
	runner *campaign.Runner,
	log *slog.Logger,
) *Server {
	return &Server{
		cfg:      cfg,
		repo:     repo,
		manager:  manager,
		hub:      hub,
		verifier: verifier,
		admin:    auth.NewAdmin(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey),
		runner:   runner,
		resolver: campaign.NewResolver(repo, manager.DeviceOnline),
		log:      log.With("component", "http"),
	}
}

// Handler returns the fully mounted router.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestTimeout(120*time.Second, 10*time.Minute))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Requested-With"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/health", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		// The WebSocket authenticates from a query parameter, so it is mounted
		// outside the header-based middleware.
		r.Get("/ws", s.handleWebSocket)

		// Signed media links carry their own credential in the URL, because an
		// <img> tag cannot send an Authorization header. Only reachable when
		// media is stored locally; inert when Supabase Storage is in use.
		r.Get("/media/{token}", s.handleServeMedia)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Get("/me", s.handleMe)

			r.Route("/applications", func(r chi.Router) {
				r.Get("/", s.handleListApplications)
				r.Post("/", s.handleCreateApplication)
				r.Patch("/{id}", s.handleUpdateApplication)
				r.Delete("/{id}", s.handleDeleteApplication)
				// The logo drawn wherever the application appears.
				r.Post("/{id}/icon", s.handleUploadApplicationIcon)
				r.Delete("/{id}/icon", s.handleDeleteApplicationIcon)
				r.With(s.requireApplicationScope).
					Get("/{id}/accounts", s.handleListApplicationAccounts)
			})

			r.Route("/accounts", func(r chi.Router) {
				r.Get("/", s.handleListAccounts)
				r.Post("/", s.handleCreateAccount)
				r.Get("/stats", s.handleAccountStats)

				// Everything below reaches one number by id, which bypasses
				// every list that narrows by application. The guard stands
				// here rather than being repeated in nine handlers.
				r.Route("/{id}", func(r chi.Router) {
					r.Use(s.requireAccountScope)
					r.Get("/", s.handleGetAccount)
					r.Patch("/", s.handleUpdateAccount)
					r.Delete("/", s.handleDeleteAccount)

					r.Post("/pair", s.handlePairAccount)
					r.Get("/qr", s.handleAccountQR)
					r.Post("/connect", s.handleConnectAccount)
					r.Post("/disconnect", s.handleDisconnectAccount)
					r.Post("/logout", s.handleLogoutAccount)
					r.Post("/sync", s.handleSyncAccount)

					r.Get("/conversations", s.handleListConversations)
					r.Get("/conversations/counts", s.handleConversationCounts)

					// The two panels beside Chat. Status is stored like any
					// other message and read back grouped by who posted it;
					// channels are read from WhatsApp on demand.
					r.Get("/status", s.handleListStatus)
					r.Get("/newsletters", s.handleListNewsletters)
					r.Post("/newsletters/sync", s.handleSyncNewsletters)
					r.Get("/newsletters/posts", s.handleNewsletterPosts)
					r.Get("/newsletters/media", s.handleNewsletterMedia)
					r.Post("/newsletters/action", s.handleNewsletterAction)
					// Posting, for a channel this number owns or administers.
					// Split the way the chat routes are: JSON for a sentence or
					// a poll, multipart for a file.
					r.Post("/newsletters/post", s.handlePostNewsletter)
					r.Post("/newsletters/media", s.handlePostNewsletterMedia)
				})
			})

			r.Route("/conversations/{id}", func(r chi.Router) {
				// Fourteen routes reach one thread by id. A check written into
				// each of them is a check that gets forgotten on the fifteenth.
				r.Use(s.requireConversationScope)
				r.Get("/", s.handleGetConversation)
				r.Patch("/", s.handleUpdateConversation)
				r.Delete("/", s.handleDeleteConversation)
				r.Post("/read", s.handleMarkConversationRead)
				r.Post("/unread", s.handleMarkConversationUnread)
				r.Get("/members", s.handleGroupMembers)
				r.Post("/members", s.handleUpdateGroupMember)
				r.Post("/group/refresh", s.handleRefreshGroup)
				r.Patch("/group", s.handleUpdateGroup)
				r.Get("/first-mention", s.handleFirstMention)
				r.Get("/messages", s.handleListMessages)
				r.Post("/messages", s.handleSendMessage)
				r.Post("/media", s.handleSendMedia)
				// A picture quick reply, sent whole. Text replies never come
				// through here — those are pasted into the composer and edited
				// before they go, which is the point of a canned answer.
				r.Post("/quick-replies/{quickReplyID}", s.handleSendQuickReply)
				r.Post("/polls", s.handleCreatePoll)
				r.Post("/labels", s.handleAssignLabel)
				r.Delete("/labels/{labelID}", s.handleUnassignLabel)
			})

			r.Route("/labels", func(r chi.Router) {
				r.Get("/", s.handleListLabels)
				r.Post("/", s.handleCreateLabel)
				r.Patch("/{id}", s.handleUpdateLabel)
				r.Delete("/{id}", s.handleDeleteLabel)
			})

			// A signed URL is minted per request and expires, so the browser
			// asks for one each time it needs to render or download a file.
			r.Get("/attachments/{id}/url", s.handleAttachmentURL)

			// These target one message rather than a conversation, so the
			// guard resolves the thread behind it before anything runs.
			r.Route("/messages/{id}", func(r chi.Router) {
				r.Use(s.requireMessageScope)
				r.Patch("/", s.handleEditMessage)
				r.Delete("/", s.handleDeleteMessage)
				r.Post("/forward", s.handleForwardMessage)
				// Answering someone in private about what they wrote in a
				// group. GET resolves where it would go; POST sends it.
				r.Get("/private-reply", s.handlePrivateReplyTarget)
				r.Post("/private-reply", s.handlePrivateReply)
				r.Post("/react", s.handleReactToMessage)
				r.Post("/vote", s.handleVotePoll)
			})

			// Opening somebody's Status. Recorded locally only: see the handler.
			r.Post("/status/{messageID}/seen", s.handleMarkStatusSeen)
			// Deleting one of our own Status posts, for everyone.
			r.Post("/status/{messageID}/revoke", s.handleRevokeStatus)

			// --- group directory ----------------------------------------
			//
			// One row per WhatsApp group rather than per thread: three of
			// our numbers in the same group is one group.
			r.Route("/groups", func(r chi.Router) {
				r.Get("/", s.handleListGroups)
				r.Get("/facets", s.handleGroupDirectoryFacets)
				// One group and its participants, for the group's own page.
				r.Get("/members", s.handleGroupDirectoryMembers)
				r.Get("/members/export", s.handleExportGroupMembers)
				r.Post("/refresh", s.handleRefreshOneGroup)
				r.Get("/export", s.handleExportGroups)
				// Pulls member lists a batch at a time and reports what is
				// left, so nine hundred groups do not become one timeout.
				r.Post("/fetch", s.handleFetchGroups)
			})

			// --- address book -------------------------------------------
			r.Route("/contacts", func(r chi.Router) {
				r.Get("/", s.handleListContacts)
				// The chip rows and their counts, read in one pass so the
				// totals cannot disagree with the list.
				r.Get("/facets", s.handleContactFacets)
				r.Get("/export", s.handleExportContacts)
				r.Post("/import", s.handleImportContacts)
				r.Post("/merge-duplicates", s.handleMergeDuplicateContacts)
				r.Post("/delete", s.handleDeleteContacts)
				r.Delete("/{id}", s.handleDeleteContact)
			})

			// --- operational reporting ---------------------------------
			//
			// Every one of these resolves the caller's scope first and
			// refuses anything outside it. The RLS policies say the same
			// for the browser's direct Supabase path; this is the copy
			// that holds on the API path, where the service role bypasses
			// RLS entirely.
			r.Get("/dashboard", s.handleDashboard)
			r.Get("/performance", s.handlePerformance)
			r.Get("/performance/team", s.handleTeamPerformance)
			// The same figures split per application, for a reader who
			// holds more than one.
			r.Get("/performance/by-application", s.handlePerformanceByApplication)
			// One full summary per person, so the per-person tables carry
			// the same columns as the day by day one.
			r.Get("/performance/members", s.handleMemberBreakdown)
			// Message volume as a curve. The Dashboard's other figures ride on
			// /dashboard and /performance above, so one number is never
			// computed twice by two screens.
			r.Get("/analytics/traffic", s.handleTraffic)
			r.Get("/analytics/filters", s.handleAnalyticsFilters)
			// One history for every kind of activity, shown inside a person's
			// performance detail rather than on a screen of its own.
			r.Get("/analytics/activity", s.handleActivityFeed)
			// Where a conversation lives, so "Masih Menunggu Balasan" can open
			// the right room — and only for somebody allowed to open it.
			r.Get("/analytics/conversation-location", s.handleConversationLocation)
			r.Get("/analytics/messages", s.handleMessageDrilldown)
			r.Get("/analytics/sla", s.handleSLADrilldown)
			r.Get("/analytics/follow-ups", s.handleFollowUpDrilldown)
			r.Get("/analytics/group-mentions", s.handleGroupDrilldown)
			r.Get("/analytics/label-events", s.handleLabelDrilldown)
			r.Get("/analytics/leads", s.handleLeadDrilldown)

			r.Route("/org", func(r chi.Router) {
				r.Get("/members", s.handleListOrgMembers)
				r.Post("/members", s.handleCreateMember)
				r.Patch("/members/{id}/role", s.handleSetOperationalRole)
				r.Patch("/members/{id}/assignments", s.handleSetMemberAssignments)
				r.Patch("/members/{id}/active", s.handleSetMemberActive)
				r.Patch("/members/{id}/password", s.handleSetMemberPassword)
			})

			r.Route("/schedules", func(r chi.Router) {
				r.Get("/", s.handleListSchedules)
				r.Post("/", s.handleUpsertSchedule)
				r.Delete("/{id}", s.handleDeleteSchedule)
			})

			// --- Broadcast & WA Story ---------------------------------------
			//
			// One resource for both. They differ in how they are delivered, not
			// in what they are: a message somebody composed, aimed at an
			// audience, scheduled, run by a worker and reported on. A second set
			// of routes would duplicate every permission rule in this file.
			r.Route("/campaigns", func(r chi.Router) {
				r.Get("/", s.handleListCampaigns)
				r.Post("/", s.handleCreateCampaign)
				// The review step: validate, deduplicate, distribute across
				// devices and render a preview — writing nothing.
				r.Post("/preview", s.handlePreviewTargets)
				// Candidate recipients for the chosen devices.
				r.Get("/audiences", s.handleListAudiences)
				r.Post("/draft", s.handleGenerateDraft)
				// A document to send with a broadcast. Images and videos travel
				// as a link; a document is uploaded, because its filename is
				// what the recipient sees.
				r.Post("/documents", s.handleUploadCampaignDocument)
				// Counts per application, for the chip row above the table.
				r.Get("/facets", s.handleCampaignFacets)

				r.Get("/{id}", s.handleGetCampaign)
				// The composer's two doors back in: read a campaign into the
				// form, and write the form back onto the same row. Neither
				// creates a copy — the old "duplicate" route did, on the way in,
				// which left a campaign behind every time somebody looked.
				r.Get("/{id}/source", s.handleCampaignSource)
				r.Put("/{id}", s.handleUpdateCampaign)
				r.Post("/{id}/schedule", s.handleScheduleCampaign)
				r.Post("/{id}/run", s.handleRunCampaign)
				r.Post("/{id}/cancel", s.handleCancelCampaign)
				r.Post("/{id}/retry", s.handleRetryCampaign)
				// Retry picks up what failed; resend sends the whole list again.
				r.Post("/{id}/resend", s.handleResendCampaign)
				// Archive is how a campaign that is not a draft leaves the list.
				// Deleting would take its send history with it.
				r.Post("/{id}/archive", s.handleArchiveCampaign)
				r.Get("/{id}/report", s.handleCampaignReport)
				// Story only: pull one number's Story off the Status display.
				// Nested under the campaign because that is where the
				// permission lives — the publication row carries no scope of
				// its own beyond the workspace.
				r.Post("/{id}/publications/{publicationID}/revoke", s.handleRevokeStoryPublication)
				r.Get("/{id}/targets", s.handleListCampaignTargets)
				r.Get("/{id}/targets/export", s.handleExportCampaignTargets)
				r.Put("/{id}/labels", s.handleSetCampaignLabels)
				r.Delete("/{id}", s.handleDeleteCampaign)
			})

			r.Route("/campaign-labels", func(r chi.Router) {
				r.Get("/", s.handleListCampaignLabels)
				r.Post("/", s.handleCreateCampaignLabel)
				r.Patch("/{id}", s.handleUpdateCampaignLabel)
			})

			r.Route("/custom-variables", func(r chi.Router) {
				r.Get("/", s.handleListCustomVariables)
				r.Post("/", s.handleUpsertCustomVariable)
				r.Delete("/{id}", s.handleDeleteCustomVariable)
			})

			// Canned replies. Read by both the settings screen and the chat
			// composer, so the list route is open to every role; writing is
			// narrowed inside the handlers.
			r.Route("/quick-replies", func(r chi.Router) {
				r.Get("/", s.handleListQuickReplies)
				r.Post("/", s.handleUpsertQuickReply)
				// The spreadsheet round trip. What export writes, import reads
				// back, so editing a set in bulk means downloading it, changing
				// it, and handing it back.
				r.Get("/export", s.handleExportQuickReplies)
				r.Post("/import", s.handleImportQuickReplies)
				// Clearing the lot. Declared before the id route so "all" is
				// never mistaken for a reply whose id happens to be missing.
				r.Delete("/", s.handleDeleteAllQuickReplies)
				// The composer reports a text reply once it has actually gone.
				// Picture replies are counted where they are sent instead.
				r.Post("/{id}/used", s.handleMarkQuickReplyUsed)
				r.Delete("/{id}", s.handleDeleteQuickReply)
			})

			// Response-time targets. Readable by everyone who is measured
			// against them; writing is narrowed inside the handlers.
			r.Route("/sla-targets", func(r chi.Router) {
				r.Get("/", s.handleListSLATargets)
				r.Post("/", s.handleUpsertSLATarget)
				r.Delete("/{id}", s.handleDeleteSLATarget)
			})
		})
	})

	return r
}

// requestTimeout bounds a request, with a longer budget for the routes that
// move files.
//
// A single deadline cannot serve both: 120 seconds is generous for an API call
// and far too short for a 64 MB video, which has to travel to this server and
// then on to WhatsApp. Nesting a longer timeout inside a shorter one does not
// work — context.WithTimeout keeps the earlier of the two deadlines — so the
// choice is made once, here, before any deadline is set.
//
// The WebSocket is excluded outright. It is a long-lived connection by design,
// and any deadline at all would eventually cut it.
func requestTimeout(normal, media time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/ws") {
				next.ServeHTTP(w, r)
				return
			}

			budget := normal
			// Anything that moves a file: uploading one, fetching one that
			// must first be pulled down from WhatsApp, forwarding one — which
			// re-uploads it once per destination — or sending a picture quick
			// reply, which downloads the image before it can send it.
			//
			// The quick-reply routes are matched on their middle segment rather
			// than their suffix, because one ends in an id. Importing belongs
			// here too: it fetches one image per row that carries a link.
			if (r.Method == http.MethodPost &&
				(strings.HasSuffix(r.URL.Path, "/media") ||
					strings.HasSuffix(r.URL.Path, "/forward") ||
					strings.Contains(r.URL.Path, "/quick-replies"))) ||
				(r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/url")) {
				budget = media
			}

			ctx, cancel := context.WithTimeout(r.Context(), budget)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	if err := s.repo.Pool().Ping(r.Context()); err != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"status": status,
		"env":    s.cfg.Env,
		"time":   time.Now().UTC(),
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	workspace, err := s.repo.GetWorkspace(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	// The operational role (Leader / PIC / Freelance) is what the person
	// recognises as "their account"; the workspace role on the user row is a
	// permission tier they never chose. Resolved here, once, from the same
	// rule every scoped endpoint uses, so the sidebar and the data agree.
	sc, err := s.repo.ResolveScope(r.Context(), user)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      user,
		"workspace": workspace,
		"scope": map[string]any{
			"role": sc.Role,
			"all":  sc.All,
		},
	})
}
