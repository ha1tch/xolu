// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Command otogen generates credentials for an xolu server configured for tenant
// access control. It implements the minting procedures described in
// docs/proposals/tenant-access-control-operations.md:
//
//	otogen jwt     — mint an HS256 JWT with tenant grants (scoped mode)
//	otogen apikey  — generate an API key and its APIKeyGrants config block
//	otogen bearer  — generate an InternalToken (bearer / trusted-gateway)
//	otogen s3key   — generate an S3 access-key/secret pair and its S3KeyGrants block
//
// The JWT signing secret is never accepted on the command line (it would leak
// into shell history and process listings). It is read from the XOLU_JWT_SECRET
// environment variable, or from a file via --secret-file.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ha1tch/xolu/pkg/s3sig"
)

const usage = `otogen — credential generator for xolu tenant access control

Usage:
  otogen <command> [flags]

Commands:
  jwt        Mint an HS256 JWT with tenant grants (scoped mode)
  apikey     Generate an API key and its APIKeyGrants config block
  bearer     Generate an InternalToken (bearer / trusted-gateway credential)
  s3key      Generate an S3 access-key/secret pair and its S3KeyGrants block
  s3sign     Produce a signed S3 request to verify an S3KeyGrant end-to-end
  help       Show this help

Run "otogen <command> -h" for command-specific flags.

All commands accept --format text|yaml|json|csv (default text). For jwt, text
emits the bare token (suitable for piping); other formats wrap it with its grant.

The JWT signing secret is read from XOLU_JWT_SECRET or --secret-file, never from a
flag (a flag value leaks into shell history and process listings).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "jwt":
		cmdJWT(os.Args[2:])
	case "apikey":
		cmdAPIKey(os.Args[2:])
	case "bearer":
		cmdBearer(os.Args[2:])
	case "s3key":
		cmdS3Key(os.Args[2:])
	case "s3sign":
		cmdS3Sign(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "otogen: unknown command %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

// ---------------------------------------------------------------------------
// jwt
// ---------------------------------------------------------------------------

func cmdJWT(args []string) {
	fs := flag.NewFlagSet("jwt", flag.ExitOnError)
	var (
		tenants    = fs.String("tenants", "", "comma-separated tenant names the token may act on (e.g. acme,globex)")
		admin      = fs.Bool("admin", false, "issue an admin token (tenant_admin: true; authorised for any tenant)")
		sub        = fs.String("sub", "", "subject claim (identity, used for logging)")
		iss        = fs.String("iss", "", "issuer claim (must match the server's JWTIssuer if it sets one)")
		ttl        = fs.Duration("ttl", time.Hour, "token lifetime; sets exp = now + ttl (exp is mandatory)")
		nbf        = fs.Duration("nbf", 0, "not-before offset from now (optional; 0 means valid immediately)")
		secretFile = fs.String("secret-file", "", "path to a file containing the JWT secret (overrides XOLU_JWT_SECRET)")
		format     = fs.String("format", "text", "output format: text (bare token), yaml, json, csv")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "otogen jwt — mint an HS256 JWT with tenant grants\n\n")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  XOLU_JWT_SECRET=... otogen jwt --tenants acme --sub user-1 --iss oldbytes --ttl 1h")
		fmt.Fprintln(os.Stderr, "  XOLU_JWT_SECRET=... otogen jwt --admin --sub ops-jane --ttl 15m")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	outFmt := parseFormat(*format)

	if *admin && *tenants != "" {
		fatal("jwt: use either --admin or --tenants, not both")
	}
	if !*admin && *tenants == "" {
		fatal("jwt: one of --tenants or --admin is required (a token with no grant is rejected on every tenant route)")
	}
	if *ttl <= 0 {
		fatal("jwt: --ttl must be positive (exp is mandatory and must be in the future)")
	}

	secret, err := loadSecret(*secretFile)
	if err != nil {
		fatal("jwt: " + err.Error())
	}

	now := time.Now()
	claims := map[string]interface{}{
		"exp": now.Add(*ttl).Unix(),
		"iat": now.Unix(),
	}
	if *sub != "" {
		claims["sub"] = *sub
	}
	if *iss != "" {
		claims["iss"] = *iss
	}
	if *nbf > 0 {
		claims["nbf"] = now.Add(*nbf).Unix()
	}
	if *admin {
		claims["tenant_admin"] = true
	} else {
		claims["tenants"] = splitCSV(*tenants)
	}

	token, err := signHS256(claims, secret)
	if err != nil {
		fatal("jwt: " + err.Error())
	}
	rec := credRecord{Kind: "jwt", Token: token, Admin: *admin}
	if !*admin {
		rec.Tenants = splitCSV(*tenants)
	}
	render(outFmt, rec)
}

// ---------------------------------------------------------------------------
// apikey
// ---------------------------------------------------------------------------

func cmdAPIKey(args []string) {
	fs := flag.NewFlagSet("apikey", flag.ExitOnError)
	var (
		tenants = fs.String("tenants", "", "comma-separated tenant names this key may act on")
		admin   = fs.Bool("admin", false, "issue an admin key (tenant_admin: true)")
		raw     = fs.Bool("raw", false, "print only the key, without any config block")
		format  = fs.String("format", "text", "output format: text, yaml, json, csv")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "otogen apikey — generate an API key and its APIKeyGrants block\n\n")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  otogen apikey --tenants acme")
		fmt.Fprintln(os.Stderr, "  otogen apikey --admin --format json")
		fmt.Fprintln(os.Stderr, "  otogen apikey --tenants acme --raw")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	outFmt := parseFormat(*format)

	if *admin && *tenants != "" {
		fatal("apikey: use either --admin or --tenants, not both")
	}
	if !*admin && *tenants == "" {
		fatal("apikey: one of --tenants or --admin is required (an ungranted key is rejected under scoped mode)")
	}

	key, err := randomToken(32)
	if err != nil {
		fatal("apikey: " + err.Error())
	}

	if *raw {
		fmt.Println(key)
		return
	}

	render(outFmt, credRecord{
		Kind:    "apikey",
		Key:     key,
		Tenants: splitCSV(*tenants),
		Admin:   *admin,
	})
}

// ---------------------------------------------------------------------------
// bearer
// ---------------------------------------------------------------------------

func cmdBearer(args []string) {
	fs := flag.NewFlagSet("bearer", flag.ExitOnError)
	raw := fs.Bool("raw", false, "print only the token, without any config hint")
	format := fs.String("format", "text", "output format: text, yaml, json, csv")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "otogen bearer — generate an InternalToken (bearer / trusted-gateway)\n\n")
		fmt.Fprintln(os.Stderr, "The bearer token is a single shared secret for the whole server. Under scoped")
		fmt.Fprintln(os.Stderr, "mode it is treated as full (admin) authority; it isolates nothing. Use it only")
		fmt.Fprintln(os.Stderr, "for a trusted gateway that does its own per-user authorization.")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	outFmt := parseFormat(*format)

	token, err := randomToken(32)
	if err != nil {
		fatal("bearer: " + err.Error())
	}
	if *raw {
		fmt.Println(token)
		return
	}
	render(outFmt, credRecord{Kind: "bearer", Token: token, InternalTok: token})
}

// ---------------------------------------------------------------------------
// s3key
// ---------------------------------------------------------------------------

func cmdS3Key(args []string) {
	fs := flag.NewFlagSet("s3key", flag.ExitOnError)
	var (
		tenants = fs.String("tenants", "", "comma-separated tenant names this S3 key may act on")
		admin   = fs.Bool("admin", false, "issue an admin S3 key (tenant_admin: true; authorised for any bucket)")
		format  = fs.String("format", "text", "output format: text, yaml, json, csv")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "otogen s3key — generate an S3 access-key/secret pair and its S3KeyGrants block\n\n")
		fmt.Fprintln(os.Stderr, "Under TenantAuthMode: scoped, the access key must have a matching S3KeyGrant")
		fmt.Fprintln(os.Stderr, "that authorises the requested bucket. The access-key string is no longer")
		fmt.Fprintln(os.Stderr, "trusted as the tenant name, and scoped mode forces S3 authentication.")
		fmt.Fprintln(os.Stderr, "\nExamples:")
		fmt.Fprintln(os.Stderr, "  otogen s3key --tenants acme")
		fmt.Fprintln(os.Stderr, "  otogen s3key --tenants acme,globex --format yaml")
		fmt.Fprintln(os.Stderr, "  otogen s3key --admin --format json")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	outFmt := parseFormat(*format)

	if *admin && *tenants != "" {
		fatal("s3key: use either --admin or --tenants, not both")
	}
	if !*admin && *tenants == "" {
		fatal("s3key: one of --tenants or --admin is required (a key with no grant is rejected under scoped mode)")
	}

	accessKey, err := randomAccessKeyID()
	if err != nil {
		fatal("s3key: " + err.Error())
	}
	secret, err := randomToken(32)
	if err != nil {
		fatal("s3key: " + err.Error())
	}

	render(outFmt, credRecord{
		Kind:      "s3key",
		AccessKey: accessKey,
		Secret:    secret,
		Tenants:   splitCSV(*tenants),
		Admin:     *admin,
	})
}

// ---------------------------------------------------------------------------
// s3sign
// ---------------------------------------------------------------------------

// cmdS3Sign produces a signed S3 request so an operator can verify an S3KeyGrant
// end-to-end against a running server without a full S3 client. The secret is
// read from --secret-file or XOLU_S3_SECRET, never from a flag.
func cmdS3Sign(args []string) {
	fs := flag.NewFlagSet("s3sign", flag.ExitOnError)
	var (
		accessKey  = fs.String("access-key", "", "S3 access key ID (required)")
		bucket     = fs.String("bucket", "", "bucket (tenant) to address (required)")
		object     = fs.String("object", "", "object key within the bucket (optional)")
		endpoint   = fs.String("endpoint", "http://localhost:9091", "server S3 endpoint base URL")
		method     = fs.String("method", "GET", "HTTP method")
		region     = fs.String("region", "us-east-1", "SigV4 region")
		secretFile = fs.String("secret-file", "", "path to a file containing the S3 secret (overrides XOLU_S3_SECRET)")
		curl       = fs.Bool("curl", false, "emit a ready-to-run curl command instead of just the header")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "otogen s3sign — produce a signed S3 request to verify an S3KeyGrant\n\n")
		fmt.Fprintln(os.Stderr, "The secret is read from --secret-file or XOLU_S3_SECRET, never from a flag.")
		fmt.Fprintln(os.Stderr, "\nExamples:")
		fmt.Fprintln(os.Stderr, "  XOLU_S3_SECRET=... otogen s3sign --access-key AKIA... --bucket acme --curl")
		fmt.Fprintln(os.Stderr, "  XOLU_S3_SECRET=... otogen s3sign --access-key AKIA... --bucket acme --object f.txt")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if *accessKey == "" {
		fatal("s3sign: --access-key is required")
	}
	if *bucket == "" {
		fatal("s3sign: --bucket is required")
	}
	secret, err := loadS3Secret(*secretFile)
	if err != nil {
		fatal("s3sign: " + err.Error())
	}

	host, err := hostFromEndpoint(*endpoint)
	if err != nil {
		fatal("s3sign: " + err.Error())
	}

	path := "/" + *bucket
	if *object != "" {
		path += "/" + *object
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	yyyymmdd := now.Format("20060102")
	const payloadHash = "UNSIGNED-PAYLOAD"

	comp := s3sig.Components{
		AccessKey:  *accessKey,
		Date:       yyyymmdd,
		Region:     *region,
		Service:    "s3",
		Method:     strings.ToUpper(*method),
		CanonURI:   path,
		CanonQuery: "",
		Headers: map[string]string{
			"host":                 host,
			"x-amz-date":           amzDate,
			"x-amz-content-sha256": payloadHash,
		},
		PayloadHash: payloadHash,
		AmzDate:     amzDate,
	}
	auth := s3sig.Sign(secret, comp, []string{"host", "x-amz-date", "x-amz-content-sha256"})

	url := strings.TrimRight(*endpoint, "/") + path
	if *curl {
		fmt.Printf("curl -X %s %q \\\n", strings.ToUpper(*method), url)
		fmt.Printf("  -H %q \\\n", "Authorization: "+auth)
		fmt.Printf("  -H %q \\\n", "X-Amz-Date: "+amzDate)
		fmt.Printf("  -H %q\n", "X-Amz-Content-Sha256: "+payloadHash)
		return
	}
	fmt.Println("# Signed request headers (valid for a few minutes):")
	fmt.Printf("Authorization: %s\n", auth)
	fmt.Printf("X-Amz-Date: %s\n", amzDate)
	fmt.Printf("X-Amz-Content-Sha256: %s\n", payloadHash)
	fmt.Printf("# Target: %s %s\n", strings.ToUpper(*method), url)
}

// loadS3Secret reads the S3 secret from --secret-file if given, else from
// XOLU_S3_SECRET. It never accepts the secret as a flag value.
func loadS3Secret(secretFile string) (string, error) {
	if secretFile != "" {
		data, err := os.ReadFile(secretFile)
		if err != nil {
			return "", fmt.Errorf("reading secret file: %w", err)
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			return "", fmt.Errorf("secret file %s is empty", secretFile)
		}
		return s, nil
	}
	s := strings.TrimSpace(os.Getenv("XOLU_S3_SECRET"))
	if s == "" {
		return "", fmt.Errorf("no secret: set XOLU_S3_SECRET or pass --secret-file")
	}
	return s, nil
}

// hostFromEndpoint extracts the host[:port] from a base URL for the SigV4 host
// header (which must match what the server sees).
func hostFromEndpoint(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("endpoint %q has no host", endpoint)
	}
	return u.Host, nil
}

// ---------------------------------------------------------------------------
// output formatting
// ---------------------------------------------------------------------------

// outputFormat is the selected rendering format for a generated credential.
type outputFormat string

const (
	formatText outputFormat = "text" // human-readable, with config-block hints (default)
	formatYAML outputFormat = "yaml" // a config snippet ready to paste
	formatJSON outputFormat = "json" // machine-readable object
	formatCSV  outputFormat = "csv"  // header + single row, for spreadsheets/pipelines
)

// parseFormat validates a --format value.
func parseFormat(s string) outputFormat {
	switch outputFormat(s) {
	case formatText, formatYAML, formatJSON, formatCSV:
		return outputFormat(s)
	default:
		fatal("--format must be one of: text, yaml, json, csv (got " + s + ")")
		return formatText // unreachable
	}
}

// credRecord is the structured form of a generated credential, rendered into
// the selected format. Fields left zero are omitted where the format allows.
type credRecord struct {
	Kind        string   // "jwt" | "apikey" | "bearer" | "s3key"
	Token       string   // jwt / bearer: the token value
	Key         string   // apikey: the key value
	AccessKey   string   // s3key: access key ID
	Secret      string   // s3key: secret access key
	InternalTok string   // bearer: the internal token (same as Token)
	Tenants     []string // grant: tenant names (empty if admin)
	Admin       bool     // grant: true if admin
}

// orderedFields returns the (name, value) pairs present for this record, in a
// stable order, for json/csv rendering. Grant info is flattened: an "admin"
// boolean column and a "tenants" column (semicolon-joined).
func (c credRecord) orderedFields() ([]string, map[string]string) {
	var names []string
	vals := map[string]string{}
	add := func(k, v string) { names = append(names, k); vals[k] = v }

	add("kind", c.Kind)
	if c.Token != "" && c.Kind != "bearer" {
		add("token", c.Token)
	}
	if c.Key != "" {
		add("key", c.Key)
	}
	if c.AccessKey != "" {
		add("access_key", c.AccessKey)
	}
	if c.Secret != "" {
		add("secret", c.Secret)
	}
	if c.InternalTok != "" {
		add("internal_token", c.InternalTok)
	}
	// Grant columns apply to credentials that carry a tenant grant.
	if c.Kind == "apikey" || c.Kind == "s3key" || c.Kind == "jwt" {
		if c.Admin {
			add("tenant_admin", "true")
			add("tenants", "")
		} else {
			add("tenant_admin", "false")
			add("tenants", strings.Join(c.Tenants, ";"))
		}
	}
	return names, vals
}

// render writes the record to stdout in the selected format.
func render(format outputFormat, c credRecord) {
	switch format {
	case formatText:
		renderText(c)
	case formatYAML:
		renderYAML(c)
	case formatJSON:
		renderJSON(c)
	case formatCSV:
		renderCSV(c)
	}
}

func renderJSON(c credRecord) {
	names, vals := c.orderedFields()
	// Build an ordered object by hand to keep field order stable and to render
	// tenants as an array rather than a joined string.
	var b strings.Builder
	b.WriteString("{\n")
	for i, n := range names {
		b.WriteString("  ")
		kb, _ := json.Marshal(n)
		b.Write(kb)
		b.WriteString(": ")
		switch n {
		case "tenant_admin":
			b.WriteString(vals[n]) // bareword true/false
		case "tenants":
			arr := c.Tenants
			if arr == nil {
				arr = []string{}
			}
			ab, _ := json.Marshal(arr)
			b.Write(ab)
		default:
			vb, _ := json.Marshal(vals[n])
			b.Write(vb)
		}
		if i < len(names)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}")
	fmt.Println(b.String())
}

func renderCSV(c credRecord) {
	names, vals := c.orderedFields()
	fmt.Println(strings.Join(names, ","))
	row := make([]string, len(names))
	for i, n := range names {
		row[i] = csvField(vals[n])
	}
	fmt.Println(strings.Join(row, ","))
}

// csvField quotes a field if it contains a comma, quote, or newline (RFC 4180).
func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// renderYAML emits the config snippet for the credential's kind, ready to paste
// into the server configuration. For jwt/bearer (single values) it emits the
// relevant scalar mapping.
func renderYAML(c credRecord) {
	switch c.Kind {
	case "jwt":
		fmt.Printf("token: %q\n", c.Token)
	case "bearer":
		fmt.Printf("InternalToken: %q\n", c.InternalTok)
	case "apikey":
		fmt.Println("APIKeyGrants:")
		fmt.Printf("  - key: %q\n", c.Key)
		renderYAMLGrant(c)
	case "s3key":
		fmt.Println("S3KeyGrants:")
		fmt.Printf("  - access_key: %q\n", c.AccessKey)
		fmt.Printf("    secret: %q\n", c.Secret)
		renderYAMLGrant(c)
	}
}

func renderYAMLGrant(c credRecord) {
	if c.Admin {
		fmt.Println("    tenant_admin: true")
	} else {
		fmt.Printf("    tenants: [%s]\n", quoteList(c.Tenants))
	}
}

// renderText emits the original human-readable output (the default), with the
// credential followed by a commented config-block hint where applicable.
func renderText(c credRecord) {
	switch c.Kind {
	case "jwt":
		fmt.Println(c.Token)
	case "bearer":
		fmt.Println("# Set as the server's InternalToken (env XOLU_INTERNAL_TOKEN):")
		fmt.Printf("InternalToken: %q\n", c.InternalTok)
		fmt.Println("#")
		fmt.Println("# Clients present:  Authorization: Bearer " + c.InternalTok)
	case "apikey":
		fmt.Println("# Add to the server's APIKeyGrants config:")
		fmt.Println("APIKeyGrants:")
		fmt.Printf("  - key: %q\n", c.Key)
		renderYAMLGrant(c)
	case "s3key":
		fmt.Println("# S3 credential")
		fmt.Printf("AccessKeyID:     %s\n", c.AccessKey)
		fmt.Printf("SecretAccessKey: %s\n", c.Secret)
		fmt.Println("#")
		fmt.Println("# Add to the server's S3KeyGrants config:")
		fmt.Println("S3KeyGrants:")
		fmt.Printf("  - access_key: %q\n", c.AccessKey)
		fmt.Printf("    secret: %q\n", c.Secret)
		renderYAMLGrant(c)
	}
}

// ---------------------------------------------------------------------------
// signing & helpers
// ---------------------------------------------------------------------------

// signHS256 builds and signs a compact JWS (JWT) with HS256, matching exactly
// what the xolu server's validator accepts: base64url(header).base64url(payload)
// signed with HMAC-SHA256 over that input using the shared secret.
func signHS256(claims map[string]interface{}, secret string) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64(hb) + "." + b64(pb)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	return signingInput + "." + b64(sig), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// loadSecret reads the JWT secret from --secret-file if given, else from
// XOLU_JWT_SECRET. It never accepts the secret as a flag value.
func loadSecret(secretFile string) (string, error) {
	if secretFile != "" {
		data, err := os.ReadFile(secretFile)
		if err != nil {
			return "", fmt.Errorf("reading secret file: %w", err)
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			return "", fmt.Errorf("secret file %s is empty", secretFile)
		}
		return s, nil
	}
	s := strings.TrimSpace(os.Getenv("XOLU_JWT_SECRET"))
	if s == "" {
		return "", fmt.Errorf("no secret: set XOLU_JWT_SECRET or pass --secret-file")
	}
	return s, nil
}

// randomToken returns a hex-encoded random string of nBytes of entropy
// (so the printed string is 2*nBytes characters).
func randomToken(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// randomAccessKeyID returns an AWS-style uppercase alphanumeric access key ID.
func randomAccessKeyID() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	const n = 20
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating access key: %w", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return "OTO" + string(out), nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, it := range items {
		q[i] = fmt.Sprintf("%q", it)
	}
	return strings.Join(q, ", ")
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "otogen: "+msg)
	os.Exit(1)
}
