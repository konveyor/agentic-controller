package controller

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// recorder captures what actually went over the wire, because a client library
// is free to read before it writes and the question here is whether one does.
type recorder struct {
	rt   http.RoundTripper
	mu   sync.Mutex
	reqs []string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	line := req.Method + " " + req.URL.Path
	if req.Body != nil && (req.Method == http.MethodPut || req.Method == http.MethodPost) {
		body, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
		if strings.Contains(string(body), `"resourceVersion"`) {
			line += " [carries resourceVersion]"
		} else {
			line += " [no resourceVersion]"
		}
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, line)
	r.mu.Unlock()
	return r.rt.RoundTrip(req)
}

// The review said an Update on an object that was never Get cannot succeed,
// because its ResourceVersion is empty. Checking that at the wire, on a real
// API server, with the client the controller uses: one PUT, no resourceVersion
// in the body, no GET before it, and it succeeds.
var _ = Describe("Update without a resourceVersion", func() {
	It("is one unconditional PUT, not a read-modify-write", func() {
		ctx := context.Background()
		rec := &recorder{}
		cfg := *cfg // envtest's rest.Config
		cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
			rec.rt = rt
			return rec
		}
		c, err := client.New(&cfg, client.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		key := types.NamespacedName{Name: "rv-wire", Namespace: "default"}
		build := func(v string) *corev1.ConfigMap {
			return &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Data:       map[string]string{skillFileKey: v},
			}
		}
		Expect(c.Create(ctx, build("original"))).To(Succeed())
		defer func() { _ = c.Delete(ctx, build("")) }()

		rec.mu.Lock()
		rec.reqs = nil
		rec.mu.Unlock()

		fresh := build("edited")
		Expect(fresh.ResourceVersion).To(BeEmpty(), "the object must be one the caller built, never read back")
		Expect(c.Update(ctx, fresh)).To(Succeed())

		rec.mu.Lock()
		got := append([]string(nil), rec.reqs...)
		rec.mu.Unlock()

		By("what went over the wire: " + strings.Join(got, " | "))
		Expect(got).To(HaveLen(1), "a single request, so nothing read the object first")
		Expect(got[0]).To(ContainSubstring("PUT"))
		Expect(got[0]).To(ContainSubstring("[no resourceVersion]"))

		var after corev1.ConfigMap
		Expect(c.Get(ctx, key, &after)).To(Succeed())
		Expect(after.Data[skillFileKey]).To(Equal("edited"))
	})

	// The same question for a patch, since the answer decides whether any write
	// verb needs a read first.
	It("is the same for a merge patch", func() {
		ctx := context.Background()
		key := types.NamespacedName{Name: "rv-verbs", Namespace: "default"}
		base := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Data:       map[string]string{skillFileKey: "original"},
		}
		Expect(k8sClient.Create(ctx, base)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, base) }()

		// Merge patch from an object that was never read.
		patched := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Data:       map[string]string{skillFileKey: "patched"},
		}
		Expect(patched.ResourceVersion).To(BeEmpty())
		Expect(k8sClient.Patch(ctx, patched, client.Merge)).To(Succeed())

		var got corev1.ConfigMap
		Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
		Expect(got.Data[skillFileKey]).To(Equal("patched"))

	})
})
