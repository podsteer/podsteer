package cmd

import (
	"github.com/podsteer/podsteer/app/adapters/k8s"
	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// credentialRefresher answers application.CredentialRefresher for the
// composition root: the adapter notices a changed credential and drops its
// client, and every other holder of per-connection state is released beside
// it, for the reason Disconnect's list exists — an assessment or a node-set
// verification made with the old credentials must not be served to the new.
//
// Not Invalidate, because Invalidate also stops the cluster's port-forwards.
// The cluster stays open here, and a forward that rebuilds its client does so
// with the credentials the operator has just written.
type credentialRefresher struct {
	adapter *k8s.Adapter
	// others is assigned once, after the services in it exist and before the
	// watcher starts, and never changed afterwards.
	others application.Invalidators
}

var _ application.CredentialRefresher = (*credentialRefresher)(nil)

func (r *credentialRefresher) CredentialsChanged(id domain.ClusterID) bool {
	return r.adapter.CredentialsChanged(id)
}

func (r *credentialRefresher) RefreshCredentials(id domain.ClusterID) {
	r.adapter.RefreshClient(id)
	r.others.Invalidate(id)
}
