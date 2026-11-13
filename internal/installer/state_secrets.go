package installer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

// The key is a separate root-only file. State and backups can be inspected
// without exposing passwords; moving a backup requires its state.key as well.
var stateSecretFields = []string{"DBPassword", "AppSecret", "MetricsPassword", "WebhookSecret", "AdminPassword", "ManagerToken", "SubToken", "CookieKey", "CookieValue", "TinyPassword", "TinyHash", "MFAPassword", "MFASigningKey"}

func stateCipher(path string, create bool) (cipher.AEAD, error) {
	keyPath := filepath.Join(filepath.Dir(path), "state.key")
	if err := noSymlinkParents(keyPath); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) && create {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = writeFile(keyPath, key, 0600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid state encryption key; restore state.key from the same backup")
	}
	return cipher.NewGCM(block)
}

func encodeState(path string, s State) ([]byte, error) {
	secrets := map[string]string{}
	value := reflect.ValueOf(&s).Elem()
	for _, name := range stateSecretFields {
		field := value.FieldByName(name)
		secrets[name] = field.String()
		field.SetString("")
	}
	if s.DetachedSubscription != nil {
		secrets["DetachedDNSAPIToken"] = s.DetachedSubscription.Certificate.Token
		if s.DetachedSubscription.RemoteSubscription != nil {
			secrets["DetachedSSHPassword"] = s.DetachedSubscription.RemoteSubscription.Password
		}
	}
	secrets["DNSAPIToken"] = s.Params.Certificate.Token
	if s.Params.RemoteSubscription != nil {
		secrets["SubscriptionSSHPassword"] = s.Params.RemoteSubscription.Password
	}
	aead, err := stateCipher(path, true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	plain, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	sealed := aead.Seal(nonce, nonce, plain, []byte("RemnaManager state v1"))
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	for _, name := range stateSecretFields {
		delete(doc, name)
	}
	doc["EncryptedSecrets"] = base64.StdEncoding.EncodeToString(sealed)
	doc["SecretsVersion"] = 1
	return json.MarshalIndent(doc, "", "  ")
}

func decodeState(path string, data []byte, s *State) error {
	if err := json.Unmarshal(data, s); err != nil {
		return err
	}
	var envelope struct {
		EncryptedSecrets string
		SecretsVersion   int
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	// Old private JSON remains readable and migrates on the next successful save.
	if envelope.SecretsVersion == 0 && envelope.EncryptedSecrets == "" {
		return nil
	}
	if envelope.SecretsVersion != 1 {
		return errors.New("unsupported state secrets version")
	}
	aead, err := stateCipher(path, false)
	if err != nil {
		return err
	}
	sealed, err := base64.StdEncoding.DecodeString(envelope.EncryptedSecrets)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return errors.New("invalid encrypted state")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("RemnaManager state v1"))
	if err != nil {
		return errors.New("state authentication failed; restore the matching panel.json and state.key")
	}
	var secrets map[string]string
	if err = json.Unmarshal(plain, &secrets); err != nil {
		return err
	}
	value := reflect.ValueOf(s).Elem()
	for _, name := range stateSecretFields {
		value.FieldByName(name).SetString(secrets[name])
	}
	if s.DetachedSubscription != nil {
		s.DetachedSubscription.Certificate.Token = secrets["DetachedDNSAPIToken"]
		if s.DetachedSubscription.RemoteSubscription != nil {
			s.DetachedSubscription.RemoteSubscription.Password = secrets["DetachedSSHPassword"]
		}
	}
	s.Params.Certificate.Token = secrets["DNSAPIToken"]
	if s.Params.RemoteSubscription != nil {
		s.Params.RemoteSubscription.Password = secrets["SubscriptionSSHPassword"]
	}
	return nil
}
