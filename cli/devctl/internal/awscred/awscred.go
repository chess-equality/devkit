package awscred

import (
	"fmt"
	"os"
	"path/filepath"
)

type Creds struct {
	Profile         string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func BuildCredentialsINI(c Creds) string {
	s := fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\n", c.Profile, c.AccessKeyID, c.SecretAccessKey)
	if c.SessionToken != "" {
		s += fmt.Sprintf("aws_session_token = %s\n", c.SessionToken)
	}
	return s
}

func BuildConfigINI(c Creds) string {
	return fmt.Sprintf("[profile %s]\nregion = %s\n", c.Profile, c.Region)
}

// WriteFiles writes credentials and config files with appropriate modes.
func WriteFiles(credPath, confPath string, creds, conf []byte) error {
	if err := os.MkdirAll(filepath.Dir(credPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(credPath, creds, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(confPath, conf, 0o600); err != nil {
		return err
	}
	return nil
}

// ExportBlock returns shell export lines for use with eval $(...).
func ExportBlock(credPath, confPath, profile string) string {
	return fmt.Sprintf("export AWS_SHARED_CREDENTIALS_FILE=%q\nexport AWS_CONFIG_FILE=%q\nexport AWS_PROFILE=%q\n", credPath, confPath, profile)
}
