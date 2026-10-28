package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	// v2 uses a new item so a token previously truncated by `security -w`
	// cannot be mistaken for a valid credential and does not carry the old
	// command-line tool's Keychain ACL.
	keychainService       = "net.guoyk.gcoredl.gcores.appToken.v2"
	legacyKeychainService = "net.guoyk.gcoredl.gcores.appToken"
	keychainAccount       = "gcores.com"
)

var errCredentialNotFound = errors.New("钥匙串中没有登录令牌")

type credentialStore interface {
	SaveInteractively() error
	Load() (string, error)
	Delete() error
}

type macOSKeychain struct{}

func (macOSKeychain) SaveInteractively() error {
	fmt.Fprint(os.Stderr, "appToken（输入不会回显）: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("读取 appToken 失败: %w", err)
	}
	fmt.Fprint(os.Stderr, "请再次粘贴 appToken: ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("再次读取 appToken 失败: %w", err)
	}

	token := normalizeToken(string(first))
	confirmation := normalizeToken(string(second))
	clear(first)
	clear(second)
	if token == "" {
		return errors.New("appToken 不能为空")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(confirmation)) != 1 {
		return errors.New("两次输入的 appToken 不一致，未保存")
	}
	if err := keychainSet(keychainService, keychainAccount, token); err != nil {
		return fmt.Errorf("保存到 macOS 钥匙串失败: %w", err)
	}

	// 立即回读校验，防止系统工具静默截断却误报成功。
	stored, err := keychainGet(keychainService, keychainAccount)
	if err != nil {
		return fmt.Errorf("回读 macOS 钥匙串失败: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(stored)) != 1 {
		return errors.New("macOS 钥匙串回读内容不一致，未能安全保存完整令牌")
	}
	// Best-effort cleanup of the old item written through `security -w`.
	// Some Keychain ACLs may require an extra confirmation, so failure here
	// must not invalidate the newly verified credential.
	_ = keychainDelete(legacyKeychainService, keychainAccount)
	return nil
}

func (macOSKeychain) Load() (string, error) {
	token, err := keychainGet(keychainService, keychainAccount)
	if err != nil {
		if errors.Is(err, errCredentialNotFound) {
			return "", errors.New("钥匙串中没有登录令牌，请先运行 gcoredl login")
		}
		return "", fmt.Errorf("读取 macOS 钥匙串失败: %w", err)
	}
	token = normalizeToken(token)
	if token == "" {
		return "", errors.New("钥匙串中的登录令牌为空，请重新运行 gcoredl login")
	}
	return token, nil
}

func (macOSKeychain) Delete() error {
	for _, service := range []string{keychainService, legacyKeychainService} {
		err := keychainDelete(service, keychainAccount)
		if err != nil && !errors.Is(err, errCredentialNotFound) {
			return fmt.Errorf("从 macOS 钥匙串删除登录令牌失败: %w", err)
		}
	}
	return nil
}

func normalizeToken(raw string) string {
	token := strings.TrimSpace(raw)
	if len(token) >= 2 {
		first, last := token[0], token[len(token)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			token = token[1 : len(token)-1]
		}
	}
	if decoded, err := url.PathUnescape(token); err == nil {
		token = decoded
	}
	return token
}
