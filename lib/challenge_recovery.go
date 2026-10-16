package lib

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/TecharoHQ/anubis"
	"github.com/TecharoHQ/anubis/internal"
	"github.com/TecharoHQ/anubis/lib/localization"
	"github.com/TecharoHQ/anubis/web"
	"github.com/a-h/templ"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var challengeRejections = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "anubis_challenge_rejections_total",
	Help: "Expected challenge rejections, distinct from internal server failures",
}, []string{"reason"})

// challengeRecoveryURL returns a local navigation through the protected site's
// normal request pipeline, never a clearance redirect to an external site.
func challengeRecoveryURL(r *http.Request, target *url.URL) string {
	if target.Host != "" && !strings.EqualFold(target.Host, r.Host) {
		return ""
	}
	if !strings.HasPrefix(target.Path, "/") || strings.HasPrefix(target.Path, "//") ||
		strings.ContainsAny(target.Path, "\\") || strings.HasPrefix(target.Path, anubis.BasePrefix+"/.within.website/") {
		return ""
	}
	for _, segment := range strings.Split(target.Path, "/") {
		if segment == "." || segment == ".." {
			return ""
		}
	}
	return target.RequestURI()
}

func (s *Server) respondWithChallengeRecovery(w http.ResponseWriter, r *http.Request, target *url.URL, reason string, status int) {
	localizer := localization.GetLocalizer(r)
	challengeRejections.WithLabelValues(reason).Inc()
	component := web.Base(
		localizer.T("verification_restart_title"),
		web.ChallengeRecovery(localizer.T("verification_"+reason), challengeRecoveryURL(r, target), localizer),
		s.policy.Impressum,
		s.policy.Honeypot,
		localizer,
	)
	internal.NoStoreCache(templ.Handler(component, templ.WithStatus(status))).ServeHTTP(w, r)
}
