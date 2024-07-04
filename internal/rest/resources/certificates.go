package resources

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/rest/types"
)

var clusterCertificatesCmd = rest.Endpoint{
	AllowedBeforeInit: true,
	Path:              "cluster/certificates/{name}",

	Put: rest.EndpointAction{Handler: clusterCertificatesPut, AccessHandler: access.AllowAuthenticated},
}

func clusterCertificatesPut(s *state.State, r *http.Request) response.Response {
	certificateName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := types.ClusterCertificatePut{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.Database.IsOpen(r.Context())
	if err != nil {
		logger.Warn(fmt.Sprintf("Database is offline, only updating local %q certificate", certificateName), logger.Ctx{"error": err})
	}

	// Forward the request to all other nodes if we are the first.
	if !client.IsNotification(r) && err == nil {
		cluster, err := s.Cluster(true)
		if err != nil {
			return response.SmartError(err)
		}

		err = cluster.Query(s.Context, true, func(ctx context.Context, c *client.Client) error {
			return c.UpdateClusterCertificate(ctx, req)
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to update %q certificate on peers: %w", certificateName, err))
		}
	}

	certBlock, _ := pem.Decode([]byte(req.PublicKey))
	if certBlock == nil {
		return response.BadRequest(fmt.Errorf("Certificate must be base64 encoded PEM certificate"))
	}

	keyBlock, _ := pem.Decode([]byte(req.PrivateKey))
	if keyBlock == nil {
		return response.BadRequest(fmt.Errorf("Private key must be base64 encoded PEM key"))
	}

	// Validate the certificate's name.
	if strings.Contains(certificateName, "/") || strings.Contains(certificateName, "\\") || strings.Contains(certificateName, "..") {
		return response.BadRequest(fmt.Errorf("Certificate name cannot be a path"))
	}

	// If a CA was specified, validate that as well.
	if req.CA != "" {
		caBlock, _ := pem.Decode([]byte(req.CA))
		if caBlock == nil {
			return response.BadRequest(fmt.Errorf("CA must be base64 encoded PEM key"))
		}

		err = os.WriteFile(filepath.Join(s.OS.StateDir, fmt.Sprintf("%s.ca", certificateName)), []byte(req.CA), 0664)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Write the keypair to the state directory.
	err = os.WriteFile(filepath.Join(s.OS.StateDir, fmt.Sprintf("%s.crt", certificateName)), []byte(req.PublicKey), 0664)
	if err != nil {
		return response.SmartError(err)
	}

	err = os.WriteFile(filepath.Join(s.OS.StateDir, fmt.Sprintf("%s.key", certificateName)), []byte(req.PrivateKey), 0600)
	if err != nil {
		return response.SmartError(err)
	}

	if certificateName == "cluster" {
		// Load the new cluster cert from the state directory on this node.
		err = state.ReloadClusterCert()
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}
