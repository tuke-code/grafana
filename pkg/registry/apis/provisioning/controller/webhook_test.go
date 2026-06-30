package controller

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	provisioning "github.com/grafana/grafana/apps/provisioning/pkg/apis/provisioning/v0alpha1"
	"github.com/grafana/grafana/apps/provisioning/pkg/repository"
)

var subscribedEvents = []string{"pull_request", "push"}

func TestWebhookOnCreate(t *testing.T) {
	tests := []struct {
		name          string
		setupClient   func(c *repository.MockWebhookClient)
		config        *provisioning.Repository
		webhookURL    string
		expectedHook  *provisioning.WebhookStatus
		expectedError error
	}{
		{
			name: "successfully create webhook",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().CreateWebhook(mock.Anything, "https://example.com/webhook", subscribedEvents, mock.Anything).
					Return(&fakeWebhookConfig{id: 123, url: "https://example.com/webhook"}, nil)
			},
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
			},
			webhookURL: "https://example.com/webhook",
			expectedHook: &provisioning.WebhookStatus{
				ID:  123,
				URL: "https://example.com/webhook",
			},
		},
		{
			name: "no webhook URL",
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					GitHub: &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
			},
			webhookURL: "",
		},
		{
			name: "error creating webhook",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().CreateWebhook(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("failed to create webhook"))
			},
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
			},
			webhookURL:    "https://example.com/webhook",
			expectedError: fmt.Errorf("failed to create webhook"),
		},
		{
			name: "no webhook when repository has no workflows",
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
			},
			webhookURL: "https://example.com/webhook",
		},
		{
			name: "no webhook when webhookDisabled is true",
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
					Webhook:   &provisioning.WebhookConfig{Disabled: true},
				},
			},
			webhookURL: "https://example.com/webhook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := repository.NewMockWebhookClient(t)
			if tt.setupClient != nil {
				tt.setupClient(client)
			}
			repo := newMockWebhookRepo(t, tt.config, tt.webhookURL, client)

			hookOps, err := webhookOnCreate(t.Context(), repo)

			if tt.expectedError != nil {
				require.Error(t, err)
				require.Equal(t, tt.expectedError.Error(), err.Error())
				require.Nil(t, hookOps)
				return
			}

			require.NoError(t, err)
			if tt.expectedHook == nil {
				require.Nil(t, hookOps)
				return
			}
			requireStatusPatch(t, hookOps, tt.expectedHook)
		})
	}
}

func TestWebhookOnUpdate(t *testing.T) {
	tests := []struct {
		name            string
		setupClient     func(c *repository.MockWebhookClient)
		config          *provisioning.Repository
		webhookURL      string
		expectedHook    *provisioning.WebhookStatus
		expectedCleanup bool
		expectedError   error
	}{
		{
			name: "successfully update webhook when webhook exists",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().GetWebhook(mock.Anything, int64(123)).
					Return(&fakeWebhookConfig{id: 123, url: "https://example.com/webhook", events: []string{"push"}}, nil)
				c.EXPECT().EditWebhook(mock.Anything, mock.MatchedBy(func(hook repository.WebhookConfig) bool {
					return hook.GetID() == 123 && hook.GetURL() == "https://example.com/webhook-updated"
				})).Return(nil)
			},
			config:     webhookConfigWithStatus(123, "https://example.com/webhook"),
			webhookURL: "https://example.com/webhook-updated",
			expectedHook: &provisioning.WebhookStatus{
				ID:               123,
				URL:              "https://example.com/webhook-updated",
				SubscribedEvents: subscribedEvents,
			},
		},
		{
			name: "create webhook when it doesn't exist",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().GetWebhook(mock.Anything, int64(123)).Return(nil, repository.ErrFileNotFound)
				c.EXPECT().CreateWebhook(mock.Anything, "https://example.com/webhook", subscribedEvents, mock.Anything).
					Return(&fakeWebhookConfig{id: 456, url: "https://example.com/webhook", events: subscribedEvents}, nil)
			},
			config:     webhookConfigWithStatus(123, "https://example.com/old-webhook"),
			webhookURL: "https://example.com/webhook",
			expectedHook: &provisioning.WebhookStatus{
				ID:               456,
				URL:              "https://example.com/webhook",
				SubscribedEvents: subscribedEvents,
			},
		},
		{
			name:       "no webhook URL provided",
			config:     &provisioning.Repository{},
			webhookURL: "",
		},
		{
			name: "error getting webhook",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().GetWebhook(mock.Anything, int64(123)).Return(nil, fmt.Errorf("failed to get webhook"))
			},
			config:        webhookConfigWithStatus(123, "https://example.com/webhook"),
			webhookURL:    "https://example.com/webhook",
			expectedError: fmt.Errorf("get webhook: failed to get webhook"),
		},
		{
			name: "error editing webhook",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().GetWebhook(mock.Anything, int64(123)).
					Return(&fakeWebhookConfig{id: 123, url: "https://example.com/webhook", events: []string{"push"}}, nil)
				c.EXPECT().EditWebhook(mock.Anything, mock.Anything).Return(fmt.Errorf("failed to edit webhook"))
			},
			config:        webhookConfigWithStatus(123, "https://example.com/webhook"),
			webhookURL:    "https://example.com/webhook-updated",
			expectedError: fmt.Errorf("edit webhook: failed to edit webhook"),
		},
		{
			name: "create webhook when webhook status is nil",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().CreateWebhook(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&fakeWebhookConfig{id: 456, url: "https://example.com/webhook", events: subscribedEvents}, nil)
			},
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
			},
			webhookURL: "https://example.com/webhook",
			expectedHook: &provisioning.WebhookStatus{
				ID:               456,
				URL:              "https://example.com/webhook",
				SubscribedEvents: subscribedEvents,
			},
		},
		{
			name: "create webhook when webhook ID is zero",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().CreateWebhook(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&fakeWebhookConfig{id: 789, url: "https://example.com/webhook", events: subscribedEvents}, nil)
			},
			config:     webhookConfigWithStatus(0, "https://example.com/webhook"),
			webhookURL: "https://example.com/webhook",
			expectedHook: &provisioning.WebhookStatus{
				ID:               789,
				URL:              "https://example.com/webhook",
				SubscribedEvents: subscribedEvents,
			},
		},
		{
			name: "error when creating webhook fails",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().CreateWebhook(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("failed to create webhook"))
			},
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
			},
			webhookURL:    "https://example.com/webhook",
			expectedError: fmt.Errorf("failed to create webhook"),
		},
		{
			name: "error on create when not found",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().GetWebhook(mock.Anything, int64(123)).Return(nil, repository.ErrFileNotFound)
				c.EXPECT().CreateWebhook(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("failed to create webhook"))
			},
			config:        webhookConfigWithStatus(123, "https://example.com/old-webhook"),
			webhookURL:    "https://example.com/webhook",
			expectedError: fmt.Errorf("failed to create webhook"),
		},
		{
			name: "no update needed when URL and events match",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().GetWebhook(mock.Anything, int64(123)).
					Return(&fakeWebhookConfig{id: 123, url: "https://example.com/webhook", events: subscribedEvents}, nil)
			},
			config:     webhookConfigWithStatus(123, "https://example.com/webhook"),
			webhookURL: "https://example.com/webhook",
		},
		{
			name: "delete webhook when workflows are removed",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().DeleteWebhook(mock.Anything, int64(123)).Return(nil)
			},
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
				Status: provisioning.RepositoryStatus{
					Webhook: &provisioning.WebhookStatus{ID: 123, URL: "https://example.com/webhook"},
				},
			},
			webhookURL:      "https://example.com/webhook",
			expectedCleanup: true,
		},
		{
			name: "no-op when no workflows and no existing webhook",
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
				},
			},
			webhookURL: "https://example.com/webhook",
		},
		{
			name: "delete stale webhook when webhookDisabled is true",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().DeleteWebhook(mock.Anything, int64(123)).Return(nil)
			},
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
					Webhook:   &provisioning.WebhookConfig{Disabled: true},
				},
				Status: provisioning.RepositoryStatus{
					Webhook: &provisioning.WebhookStatus{ID: 123, URL: "https://example.com/webhook"},
				},
			},
			expectedCleanup: true,
		},
		{
			name: "no-op when webhookDisabled is true and no existing webhook",
			config: &provisioning.Repository{
				Spec: provisioning.RepositorySpec{
					Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
					GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
					Webhook:   &provisioning.WebhookConfig{Disabled: true},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := repository.NewMockWebhookClient(t)
			if tt.setupClient != nil {
				tt.setupClient(client)
			}
			repo := newMockWebhookRepo(t, tt.config, tt.webhookURL, client)

			hookOps, err := webhookOnUpdate(t.Context(), repo)

			if tt.expectedError != nil {
				require.Error(t, err)
				require.Equal(t, tt.expectedError.Error(), err.Error())
				require.Nil(t, hookOps)
				return
			}

			require.NoError(t, err)
			switch {
			case tt.expectedHook != nil:
				requireStatusPatch(t, hookOps, tt.expectedHook)
			case tt.expectedCleanup:
				require.Len(t, hookOps, 1)
				require.Equal(t, "replace", hookOps[0]["op"])
				require.Equal(t, "/status/webhook", hookOps[0]["path"])
				require.Nil(t, hookOps[0]["value"])
			default:
				require.Nil(t, hookOps)
			}
		})
	}
}

func TestWebhookOnDelete(t *testing.T) {
	tests := []struct {
		name          string
		setupClient   func(c *repository.MockWebhookClient)
		config        *provisioning.Repository
		expectedError error
	}{
		{
			name: "successfully delete webhook",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().DeleteWebhook(mock.Anything, int64(123)).Return(nil)
			},
			config: deleteConfig(123),
		},
		{
			name: "webhook not found during deletion",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().DeleteWebhook(mock.Anything, int64(123)).Return(repository.ErrFileNotFound)
			},
			config: deleteConfig(123),
		},
		{
			name: "unauthorized to delete the webhook",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().DeleteWebhook(mock.Anything, int64(123)).Return(repository.ErrUnauthorized)
			},
			config: deleteConfig(123),
		},
		{
			name: "webhook not found in status",
			config: &provisioning.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "test-repo"},
				Spec:       provisioning.RepositorySpec{GitHub: &provisioning.GitHubRepositoryConfig{Branch: "main"}},
			},
		},
		{
			name: "error deleting webhook",
			setupClient: func(c *repository.MockWebhookClient) {
				c.EXPECT().DeleteWebhook(mock.Anything, int64(123)).Return(fmt.Errorf("failed to delete webhook"))
			},
			config:        deleteConfig(123),
			expectedError: fmt.Errorf("delete webhook: failed to delete webhook"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := repository.NewMockWebhookClient(t)
			if tt.setupClient != nil {
				tt.setupClient(client)
			}
			repo := newMockWebhookRepo(t, tt.config, "", client)

			err := webhookOnDelete(t.Context(), repo)

			if tt.expectedError != nil {
				require.Error(t, err)
				require.Equal(t, tt.expectedError.Error(), err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRotateWebhookSecret(t *testing.T) {
	t.Run("successful rotation returns status and secure patch ops", func(t *testing.T) {
		client := repository.NewMockWebhookClient(t)
		client.EXPECT().GetWebhook(mock.Anything, int64(123)).
			Return(&fakeWebhookConfig{id: 123, url: "https://example.com/hook", events: []string{"push"}}, nil)
		client.EXPECT().EditWebhook(mock.Anything, mock.MatchedBy(func(cfg repository.WebhookConfig) bool {
			return cfg.GetID() == 123 && cfg.GetSecret() != ""
		})).Return(nil)

		repo := newMockWebhookRepo(t, rotateConfig(123), "", client)

		ops, err := rotateWebhookSecret(t.Context(), repo)
		require.NoError(t, err)
		require.Len(t, ops, 2)
		require.Equal(t, "replace", ops[0]["op"])
		require.Equal(t, "/status/webhook", ops[0]["path"])
		require.Equal(t, "replace", ops[1]["op"])
		require.Equal(t, "/secure/webhookSecret", ops[1]["path"])

		webhookStatus := ops[0]["value"].(*provisioning.WebhookStatus)
		require.True(t, webhookStatus.LastRotated > 0)
	})

	t.Run("webhook not found on remote clears status and returns error", func(t *testing.T) {
		client := repository.NewMockWebhookClient(t)
		client.EXPECT().GetWebhook(mock.Anything, int64(123)).Return(nil, repository.ErrFileNotFound)

		repo := newMockWebhookRepo(t, rotateConfig(123), "", client)

		ops, err := rotateWebhookSecret(t.Context(), repo)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found on remote")
		require.Len(t, ops, 1)
		require.Equal(t, "replace", ops[0]["op"])
		require.Equal(t, "/status/webhook", ops[0]["path"])
		require.Nil(t, ops[0]["value"])
	})

	t.Run("get webhook error returns error", func(t *testing.T) {
		client := repository.NewMockWebhookClient(t)
		client.EXPECT().GetWebhook(mock.Anything, int64(123)).Return(nil, fmt.Errorf("api error"))

		repo := newMockWebhookRepo(t, rotateConfig(123), "", client)

		ops, err := rotateWebhookSecret(t.Context(), repo)
		require.Error(t, err)
		require.Contains(t, err.Error(), "get webhook for rotation")
		require.Nil(t, ops)
	})

	t.Run("edit webhook error returns error", func(t *testing.T) {
		client := repository.NewMockWebhookClient(t)
		client.EXPECT().GetWebhook(mock.Anything, int64(123)).
			Return(&fakeWebhookConfig{id: 123, url: "https://example.com/hook"}, nil)
		client.EXPECT().EditWebhook(mock.Anything, mock.Anything).Return(fmt.Errorf("edit failed"))

		repo := newMockWebhookRepo(t, rotateConfig(123), "", client)

		ops, err := rotateWebhookSecret(t.Context(), repo)
		require.Error(t, err)
		require.Contains(t, err.Error(), "edit webhook during rotation")
		require.Nil(t, ops)
	})

	t.Run("skips when no webhook exists", func(t *testing.T) {
		repo := newMockWebhookRepo(t, &provisioning.Repository{}, "", repository.NewMockWebhookClient(t))

		ops, err := rotateWebhookSecret(t.Context(), repo)
		require.NoError(t, err)
		require.Nil(t, ops)
	})

	t.Run("skips when webhook ID is zero", func(t *testing.T) {
		repo := newMockWebhookRepo(t, rotateConfig(0), "", repository.NewMockWebhookClient(t))

		ops, err := rotateWebhookSecret(t.Context(), repo)
		require.NoError(t, err)
		require.Nil(t, ops)
	})
}

func newMockWebhookRepo(t *testing.T, config *provisioning.Repository, webhookURL string, client repository.WebhookClient) *repository.MockWebhookRepository {
	r := repository.NewMockWebhookRepository(t)
	r.EXPECT().Config().Return(config).Maybe()
	r.EXPECT().WebhookURL().Return(webhookURL).Maybe()
	r.EXPECT().SubscribedEvents().Return(subscribedEvents).Maybe()
	r.EXPECT().WebhookClient().Return(client).Maybe()
	return r
}

func requireStatusPatch(t *testing.T, ops []map[string]any, expected *provisioning.WebhookStatus) {
	t.Helper()
	require.Len(t, ops, 2)
	require.Equal(t, "replace", ops[0]["op"])
	require.Equal(t, "/status/webhook", ops[0]["path"])
	status := ops[0]["value"].(*provisioning.WebhookStatus)
	require.Equal(t, expected.ID, status.ID)
	require.Equal(t, expected.URL, status.URL)
	require.ElementsMatch(t, expected.SubscribedEvents, status.SubscribedEvents)

	require.Equal(t, "replace", ops[1]["op"])
	require.Equal(t, "/secure/webhookSecret", ops[1]["path"])
	vals, ok := ops[1]["value"].(map[string]string)
	require.True(t, ok, "expected webhookSecret as map")
	require.Len(t, vals, 1)
	require.NotEmpty(t, vals["create"])
	_, err := uuid.Parse(vals["create"])
	require.NoError(t, err, "the secret is a valid UUID")
}

func webhookConfigWithStatus(id int64, url string) *provisioning.Repository {
	return &provisioning.Repository{
		Spec: provisioning.RepositorySpec{
			Workflows: []provisioning.Workflow{provisioning.WriteWorkflow},
			GitHub:    &provisioning.GitHubRepositoryConfig{Branch: "main"},
		},
		Status: provisioning.RepositoryStatus{
			Webhook: &provisioning.WebhookStatus{ID: id, URL: url},
		},
	}
}

func deleteConfig(id int64) *provisioning.Repository {
	return &provisioning.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test-repo"},
		Spec:       provisioning.RepositorySpec{GitHub: &provisioning.GitHubRepositoryConfig{Branch: "main"}},
		Status: provisioning.RepositoryStatus{
			Webhook: &provisioning.WebhookStatus{ID: id, URL: "https://example.com/webhook"},
		},
	}
}

func rotateConfig(id int64) *provisioning.Repository {
	return &provisioning.Repository{
		Spec:   provisioning.RepositorySpec{GitHub: &provisioning.GitHubRepositoryConfig{Branch: "main"}},
		Status: provisioning.RepositoryStatus{Webhook: &provisioning.WebhookStatus{ID: id}},
	}
}

type fakeWebhookConfig struct {
	id     int64
	url    string
	events []string
	secret string
}

func (c *fakeWebhookConfig) GetID() int64              { return c.id }
func (c *fakeWebhookConfig) GetURL() string            { return c.url }
func (c *fakeWebhookConfig) GetEvents() []string       { return c.events }
func (c *fakeWebhookConfig) GetSecret() string         { return c.secret }
func (c *fakeWebhookConfig) SetURL(url string)         { c.url = url }
func (c *fakeWebhookConfig) SetEvents(events []string) { c.events = events }
func (c *fakeWebhookConfig) SetSecret(secret string)   { c.secret = secret }
