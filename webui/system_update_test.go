package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"diana-qq-bot/model/updater"

	"github.com/gin-gonic/gin"
)

type fakeSystemUpdater struct {
	status            updater.Status
	checkStatus       updater.Status
	result            updater.Result
	statusErr         error
	checkErr          error
	updateErr         error
	statusCalls       int
	checkCalls        int
	updateCalls       int
	updateContextErr  error
	updateHasDeadline bool
}

func (f *fakeSystemUpdater) Status(context.Context) (updater.Status, error) {
	f.statusCalls++
	return f.status, f.statusErr
}

func (f *fakeSystemUpdater) Check(context.Context) (updater.Status, error) {
	f.checkCalls++
	if f.checkErr != nil {
		return updater.Status{}, f.checkErr
	}
	return f.checkStatus, nil
}

func (f *fakeSystemUpdater) Update(ctx context.Context) (updater.Result, error) {
	f.updateCalls++
	f.updateContextErr = ctx.Err()
	_, f.updateHasDeadline = ctx.Deadline()
	return f.result, f.updateErr
}

func TestSystemUpdateHandlerStatus(t *testing.T) {
	fake := &fakeSystemUpdater{
		status: updater.Status{
			Root:           "/tmp/repo",
			Branch:         "main",
			RemoteName:     "origin",
			RemoteURL:      "https://github.com/example/repo.git",
			HeadCommit:     "abc1234",
			Dirty:          true,
			ApplySupported: true,
		},
	}
	router := systemUpdateTestRouter(NewSystemUpdateHandler(fake))

	rec := performSystemUpdateRequest(router, http.MethodGet, "/api/system/update")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload updater.Status
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload.Root != "/tmp/repo" || payload.Branch != "main" || !payload.Dirty || !payload.ApplySupported {
		t.Fatalf("payload = %#v", payload)
	}
	if fake.statusCalls != 1 || fake.checkCalls != 0 {
		t.Fatalf("calls: status=%d check=%d", fake.statusCalls, fake.checkCalls)
	}
}

func TestSystemUpdateHandlerRefreshesRemoteOnRequest(t *testing.T) {
	fake := &fakeSystemUpdater{
		checkStatus: updater.Status{Root: "/tmp/repo", Branch: "main", Behind: 2, UpdateAvailable: true},
	}
	router := systemUpdateTestRouter(NewSystemUpdateHandler(fake))

	rec := performSystemUpdateRequest(router, http.MethodGet, "/api/system/update?refresh=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload updater.Status
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if fake.checkCalls != 1 || fake.statusCalls != 0 || payload.Behind != 2 || !payload.UpdateAvailable {
		t.Fatalf("calls: status=%d check=%d payload=%#v", fake.statusCalls, fake.checkCalls, payload)
	}
}

func TestSystemUpdateHandlerMapsUpdateErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "missing remote", err: updater.ErrRemoteNotConfigured, want: http.StatusBadRequest},
		{name: "dirty work tree", err: updater.ErrWorkingTreeDirty, want: http.StatusConflict},
		{name: "concurrent update", err: updater.ErrUpdateInProgress, want: http.StatusConflict},
		{name: "diverged branch", err: updater.ErrNonFastForward, want: http.StatusConflict},
		{name: "fetch failure", err: fmtWrapped(updater.ErrFetchFailed), want: http.StatusBadGateway},
		{name: "apply failure", err: fmtWrapped(updater.ErrApplyFailed), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeSystemUpdater{updateErr: test.err}
			router := systemUpdateTestRouter(NewSystemUpdateHandler(fake))
			rec := performSystemUpdateRequest(router, http.MethodPost, "/api/system/update")
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.want, rec.Body.String())
			}
		})
	}
}

func TestSystemUpdateHandlerMapsCheckErrors(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: updater.ErrUpdateInProgress, want: http.StatusConflict},
		{err: updater.ErrRepositoryNotFound, want: http.StatusBadRequest},
		{err: fmtWrapped(updater.ErrFetchFailed), want: http.StatusBadGateway},
		{err: errors.New("unexpected"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		fake := &fakeSystemUpdater{checkErr: test.err}
		router := systemUpdateTestRouter(NewSystemUpdateHandler(fake))
		rec := performSystemUpdateRequest(router, http.MethodGet, "/api/system/update?refresh=true")
		if rec.Code != test.want {
			t.Fatalf("error=%v status=%d want=%d body=%s", test.err, rec.Code, test.want, rec.Body.String())
		}
	}
}

func TestSystemUpdateHandlerUpdate(t *testing.T) {
	now := time.Now()
	fake := &fakeSystemUpdater{
		result: updater.Result{
			Status: updater.Status{
				Root:            "/tmp/repo",
				Branch:          "main",
				RemoteName:      "origin",
				RemoteURL:       "https://github.com/example/repo.git",
				RestartRequired: true,
			},
			Fetched:         true,
			Updated:         true,
			SourceUpdated:   true,
			Applied:         true,
			RestartRequired: true,
			PreviousCommit:  "abc",
			TargetCommit:    "def",
			Output:          "Update applied",
			At:              now,
		},
	}
	router := systemUpdateTestRouter(NewSystemUpdateHandler(fake))

	rec := performSystemUpdateRequest(router, http.MethodPost, "/api/system/update")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload updater.Result
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !payload.Fetched || !payload.Updated || !payload.SourceUpdated || !payload.Applied || !payload.RestartRequired {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.PreviousCommit != "abc" || payload.TargetCommit != "def" || fake.updateCalls != 1 {
		t.Fatalf("payload = %#v, update calls = %d", payload, fake.updateCalls)
	}
}

func TestSystemUpdateHandlerContinuesAfterRequestCancellation(t *testing.T) {
	fake := &fakeSystemUpdater{}
	router := systemUpdateTestRouter(NewSystemUpdateHandler(fake))
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/system/update", nil).WithContext(requestContext)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if fake.updateContextErr != nil || !fake.updateHasDeadline {
		t.Fatalf("update context err = %v, has deadline = %v", fake.updateContextErr, fake.updateHasDeadline)
	}
}

func fmtWrapped(err error) error {
	return errors.Join(errors.New("operation failed"), err)
}

func performSystemUpdateRequest(router http.Handler, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func systemUpdateTestRouter(handler *SystemUpdateHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.Register(router)
	return router
}
