package okta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-mux/tf5muxserver"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/okta/okta-sdk-golang/v3/okta"
	"github.com/okta/terraform-provider-okta/sdk"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

var (
	testAccProvidersFactories       map[string]func() (*schema.Provider, error)
	testAccProtoV5ProviderFactories map[string]func() (tfprotov5.ProviderServer, error)
	testAccMergeProvidersFactories  map[string]func() (tfprotov5.ProviderServer, error)
	testSdkV3Client                 *okta.APIClient
	testSdkV2Client                 *sdk.Client
	testSdkSupplementClient         *sdk.APISupplement

	// NOTE: Our VCR set up is inspired by terraform-provider-google
	providerConfigsLock = sync.RWMutex{}
	providerConfigs     map[string]*Config
)

func init() {
	pluginProvider := Provider()
	testAccProvidersFactories = map[string]func() (*schema.Provider, error){
		"okta": func() (*schema.Provider, error) {
			return pluginProvider, nil
		},
	}
	frameworkProvider := NewFWProvider("dev")
	testAccProtoV5ProviderFactories = map[string]func() (tfprotov5.ProviderServer, error){
		"okta": providerserver.NewProtocol5WithError(frameworkProvider),
	}
	providers := []func() tfprotov5.ProviderServer{
		// v2 plugin
		pluginProvider.GRPCProvider,
		// v3 plugin
		providerserver.NewProtocol5(frameworkProvider),
	}
	muxServer, err := tf5muxserver.NewMuxServer(context.Background(), providers...)
	if err != nil {
		log.Fatalf(err.Error())
	}
	testAccMergeProvidersFactories = map[string]func() (tfprotov5.ProviderServer, error){
		"okta": func() (tfprotov5.ProviderServer, error) {
			return muxServer.ProviderServer(), nil
		},
	}

	providerConfigs = make(map[string]*Config)
}

// TestMain overridden main testing function. Package level BeforeAll and AfterAll.
// It also delineates between acceptance tests and unit tests
func TestMain(m *testing.M) {
	// TF_VAR_hostname allows the real hostname to be scripted into the config tests
	// see examples/okta_resource_set/basic.tf
	os.Setenv("TF_VAR_hostname", fmt.Sprintf("%s.%s", os.Getenv("OKTA_ORG_NAME"), os.Getenv("OKTA_BASE_URL")))

	// NOTE: Acceptance test sweepers are necessary to prevent dangling
	// resources.
	// NOTE: Don't run sweepers if we are playing back VCR as nothing should be
	// going over the wire
	if os.Getenv("OKTA_VCR_TF_ACC") != "play" {
		setupSweeper(adminRoleCustom, sweepCustomRoles)
		setupSweeper("okta_*_app", sweepTestApps)
		setupSweeper(authServer, sweepAuthServers)
		setupSweeper(behavior, sweepBehaviors)
		setupSweeper(emailCustomization, sweepEmailCustomization)
		setupSweeper(groupRule, sweepGroupRules)
		setupSweeper("okta_*_idp", sweepTestIdps)
		setupSweeper(inlineHook, sweepInlineHooks)
		setupSweeper(group, sweepGroups)
		setupSweeper(groupSchemaProperty, sweepGroupCustomSchema)
		setupSweeper(linkDefinition, sweepLinkDefinitions)
		setupSweeper(networkZone, sweepNetworkZones)
		setupSweeper(policyMfa, sweepMfaPolicies)
		setupSweeper(policyPassword, sweepPasswordPolicies)
		setupSweeper(policyRuleIdpDiscovery, sweepPolicyRuleIdpDiscovery)
		setupSweeper(policyRuleMfa, sweepMfaPolicyRules)
		setupSweeper(policyRulePassword, sweepPolicyRulePasswords)
		setupSweeper(policyRuleSignOn, sweepSignOnPolicyRules)
		setupSweeper(policySignOn, sweepAccessPolicies)
		// setupSweeper(policySignOn, sweepSignOnPolicies)
		setupSweeper(resourceSet, sweepResourceSets)
		setupSweeper(user, sweepUsers)
		setupSweeper(userSchemaProperty, sweepUserCustomSchema)
		setupSweeper(userType, sweepUserTypes)
	}

	resource.TestMain(m)
}

func TestProvider(t *testing.T) {
	if err := Provider().InternalValidate(); err != nil {
		t.Fatalf("err: %s", err)
	}
}

func TestProvider_impl(t *testing.T) {
	_ = Provider()
}

func testAccPreCheck(t *testing.T) func() {
	return func() {
		err := accPreCheck()
		if err != nil {
			t.Fatalf("%v", err)
		}
	}
}

func accPreCheck() error {
	if v := os.Getenv("OKTA_ORG_NAME"); v == "" {
		return errors.New("OKTA_ORG_NAME must be set for acceptance tests")
	}
	token := os.Getenv("OKTA_API_TOKEN")
	clientID := os.Getenv("OKTA_API_CLIENT_ID")
	privateKey := os.Getenv("OKTA_API_PRIVATE_KEY")
	privateKeyId := os.Getenv("OKTA_API_PRIVATE_KEY_IE")
	scopes := os.Getenv("OKTA_API_SCOPES")
	if token == "" && (clientID == "" || scopes == "" || privateKey == "" || privateKeyId == "") {
		return errors.New("either OKTA_API_TOKEN or OKTA_API_CLIENT_ID, OKTA_API_SCOPES and OKTA_API_PRIVATE_KEY must be set for acceptance tests")
	}
	return nil
}

func TestProviderValidate(t *testing.T) {
	envKeys := []string{
		"OKTA_ACCESS_TOKEN",
		"OKTA_ALLOW_LONG_RUNNING_ACC_TEST",
		"OKTA_API_CLIENT_ID",
		"OKTA_API_PRIVATE_KEY",
		"OKTA_API_PRIVATE_KEY_ID",
		"OKTA_API_PRIVATE_KEY_IE",
		"OKTA_API_SCOPES",
		"OKTA_API_TOKEN",
		"OKTA_BASE_URL",
		"OKTA_DEFAULT",
		"OKTA_GROUP",
		"OKTA_HTTP_PROXY",
		"OKTA_ORG_NAME",
		"OKTA_UPDATE",
	}
	envVals := make(map[string]string)
	// save and clear OKTA env vars so config can be test cleanly
	for _, key := range envKeys {
		val := os.Getenv(key)
		if val == "" {
			continue
		}
		envVals[key] = val
		os.Unsetenv(key)
	}

	tests := []struct {
		name         string
		accessToken  string
		apiToken     string
		clientID     string
		privateKey   string
		privateKeyID string
		scopes       []interface{}
		expectError  bool
	}{
		{"simple pass", "", "", "", "", "", []interface{}{}, false},
		{"access_token pass", "accessToken", "", "", "", "", []interface{}{}, false},
		{"access_token fail 1", "accessToken", "apiToken", "", "", "", []interface{}{}, true},
		{"access_token fail 2", "accessToken", "", "cliendID", "", "", []interface{}{}, true},
		{"access_token fail 3", "accessToken", "", "", "privateKey", "", []interface{}{}, true},
		{"access_token fail 4", "accessToken", "", "", "", "", []interface{}{"scope1", "scope2"}, true},
		{"api_token pass", "", "apiToken", "", "", "", []interface{}{}, false},
		{"api_token fail 1", "accessToken", "apiToken", "", "", "", []interface{}{}, true},
		{"api_token fail 2", "", "apiToken", "clientID", "", "", []interface{}{}, true},
		{"api_token fail 3", "", "apiToken", "", "", "privateKey", []interface{}{}, true},
		{"api_token fail 4", "", "apiToken", "", "", "", []interface{}{"scope1", "scope2"}, true},
		{"client_id pass", "", "", "clientID", "", "", []interface{}{}, false},
		{"client_id fail 1", "accessToken", "", "clientID", "", "", []interface{}{}, true},
		{"client_id fail 2", "accessToken", "apiToken", "clientID", "", "", []interface{}{}, true},
		{"private_key pass", "", "", "", "privateKey", "", []interface{}{}, false},
		{"private_key fail 1", "accessToken", "", "", "privateKey", "", []interface{}{}, true},
		{"private_key fail 2", "", "apiToken", "", "privateKey", "", []interface{}{}, true},
		{"private_key_id pass", "", "", "", "", "privateKeyID", []interface{}{}, false},
		{"private_key_id fail 1", "", "apiToken", "", "", "privateKeyID", []interface{}{}, true},
		{"scopes pass", "", "", "", "", "", []interface{}{"scope1", "scope2"}, false},
		{"scopes fail 1", "accessToken", "", "", "", "", []interface{}{"scope1", "scope2"}, true},
		{"scopes fail 2", "", "apiToken", "", "", "", []interface{}{"scope1", "scope2"}, true},
	}

	for _, test := range tests {
		resourceConfig := map[string]interface{}{}
		if test.accessToken != "" {
			resourceConfig["access_token"] = test.accessToken
		}
		if test.apiToken != "" {
			resourceConfig["api_token"] = test.apiToken
		}
		if test.clientID != "" {
			resourceConfig["client_id"] = test.clientID
		}
		if test.privateKey != "" {
			resourceConfig["private_key"] = test.privateKey
		}
		if test.privateKeyID != "" {
			resourceConfig["private_key_id"] = test.privateKeyID
		}
		if len(test.scopes) > 0 {
			resourceConfig["scopes"] = test.scopes
		}

		config := terraform.NewResourceConfigRaw(resourceConfig)
		provider := Provider()
		err := provider.Validate(config)

		if test.expectError && err == nil {
			t.Errorf("test %q: expected error but received none", test.name)
		}
		if !test.expectError && err != nil {
			t.Errorf("test %q: did not expect error but received error: %+v", test.name, err)
			fmt.Println()
		}
	}

	for key, val := range envVals {
		os.Setenv(key, val)
	}
}

func sdkV3ClientForTest() *okta.APIClient {
	if testSdkV3Client != nil {
		return testSdkV3Client
	}
	return sdkV3Client
}

func sdkV2ClientForTest() *sdk.Client {
	if testSdkV2Client != nil {
		return testSdkV2Client
	}
	return sdkV2Client
}

func sdkSupplementClientForTest() *sdk.APISupplement {
	if testSdkSupplementClient != nil {
		return testSdkSupplementClient
	}
	return sdkSupplementClient
}

// oktaResourceTest is the entry to overriding the Terraform SDKs Acceptance
// Test framework before the call to resource.Test
func oktaResourceTest(t *testing.T, c resource.TestCase) {
	// plug in the VCR
	mgr := newVCRManager(t.Name())

	if !mgr.VCREnabled() {
		// live ACC / non-VCR test
		resource.Test(t, c)
		return
	}

	if !mgr.ValidMode() {
		t.Fatalf("ENV variable OKTA_VCR_TF_ACC value should be %q or %q but was %q", "play", "record", mgr.VCRModeName)
		return
	}

	// sdk client clients are set up in provider factories for test
	if mgr.IsPlaying() {
		if !mgr.HasCassettesToPlay() {
			t.Skipf("%q test is missing VCR cassette(s) at %q, skipping test. See .github/CONTRIBUTING.md#acceptance-tests-with-vcr for more information about playing/recording cassettes.", t.Name(), mgr.CassettesPath)
			return
		}

		cassettes := mgr.Cassettes()
		for _, cassette := range cassettes {
			// need to artifically set expected OKTA env vars if VCR is playing
			// VCR re-writes the name [cassette].oktapreview.com
			os.Setenv("OKTA_ORG_NAME", cassette)
			os.Setenv("OKTA_BASE_URL", "oktapreview.com")
			os.Setenv("OKTA_API_TOKEN", "token")
			mgr.SetCurrentCassette(cassette)
			c.ProviderFactories = vcrProviderFactoriesForTest(mgr)
			c.CheckDestroy = nil
			fmt.Printf("=== VCR PLAY CASSETTE %q for %s\n", cassette, t.Name())
			resource.Test(t, c)
		}
		return
	}

	if mgr.IsRecording() {
		defer closeRecorder(t, mgr)
		if mgr.AttemptedWriteIsMissingCassetteName() {
			t.Fatalf("%q test is attempting to write cassette to %q, but OKTA_VCR_CASSETTE ENV var is missing. See .github/CONTRIBUTING.md#acceptance-tests-with-vcr for more information.", t.Name(), mgr.CassettePath())
			return
		}
		if mgr.AttemptedWriteOfExistingCassette() {
			t.Skipf("%q test is attempting to write %s.yaml cassette, delete it first before attempting new write, skipping test. See .github/CONTRIBUTING.md#acceptance-tests-with-vcr for more information.", t.Name(), mgr.CassettePath())
			return
		}

		c.ProviderFactories = vcrProviderFactoriesForTest(mgr)
		fmt.Printf("=== VCR RECORD CASSETTE %q for %s\n", mgr.CurrentCassette, t.Name())
		resource.Test(t, c)
		return
	}
}

// vcrProviderFactoriesForTest Returns the overriden provider factories used by the
// resource test case given the state of the VCR manager.
func vcrProviderFactoriesForTest(mgr *vcrManager) map[string]func() (*schema.Provider, error) {
	return map[string]func() (*schema.Provider, error){
		"okta": func() (*schema.Provider, error) {
			provider := Provider()
			oldConfigureContextFunc := provider.ConfigureContextFunc
			provider.ConfigureContextFunc = func(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
				config, _diag := vcrCachedConfig(ctx, d, oldConfigureContextFunc, mgr)
				if _diag.HasError() {
					return nil, _diag
				}

				testSdkV3Client = config.oktaSDKClientV3
				testSdkV2Client = config.oktaSDKClientV2
				testSdkSupplementClient = config.oktaSDKsupplementClient

				return config, _diag
			}

			return provider, nil
		},
	}
}

// We need to hijack the provider's ConfigureContextFunc as it is called many
// times during an operation which has the side effect of resetting config
// values such as the http client that okta-sdk-golang utilizes. Instead, we
// want to create one dedicated VCR http transport for each test. The dedicated
// transport holds the API calls made specific to that test.
func vcrCachedConfig(ctx context.Context, d *schema.ResourceData, configureFunc schema.ConfigureContextFunc, mgr *vcrManager) (*Config, diag.Diagnostics) {
	providerConfigsLock.RLock()
	v, ok := providerConfigs[mgr.TestAndCassetteNameKey()]
	providerConfigsLock.RUnlock()
	if ok {
		// config is cached, proceed
		return v, nil
	}

	c, diags := configureFunc(ctx, d)

	if diags.HasError() {
		return nil, diags
	}

	config := c.(*Config)
	config.SetTimeOperations(NewTestTimeOperations())

	transport := config.oktaSDKClientV3.GetConfig().HTTPClient.Transport

	rec, err := recorder.NewWithOptions(&recorder.Options{
		CassetteName:       mgr.CassettePath(),
		Mode:               mgr.VCRMode(),
		SkipRequestLatency: true, // skip how vcr will mimic the real request latency that it can record allowing for fast playback
		RealTransport:      transport,
	})
	if err != nil {
		return nil, diag.FromErr(err)
	}

	// Defines how VCR will match requests to responses.
	rec.SetMatcher(func(r *http.Request, i cassette.Request) bool {
		// Default matcher compares method and URL only
		if !cassette.DefaultMatcher(r, i) {
			return false
		}
		// TODO: there might be header inform would could to inspect to make this more precise
		if r.Body == nil {
			return true
		}

		var b bytes.Buffer
		if _, err := b.ReadFrom(r.Body); err != nil {
			log.Printf("[DEBUG] Failed to read request body from cassette: %v", err)
			return false
		}
		r.Body = io.NopCloser(&b)
		reqBody := b.String()
		// If body matches identically, we are done
		if reqBody == i.Body {
			return true
		}

		// JSON might be the same, but reordered. Try parsing json and comparing
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			var reqJson, cassetteJson interface{}
			if err := json.Unmarshal([]byte(reqBody), &reqJson); err != nil {
				log.Printf("[DEBUG] Failed to unmarshall request json: %v", err)
				return false
			}
			if err := json.Unmarshal([]byte(i.Body), &cassetteJson); err != nil {
				log.Printf("[DEBUG] Failed to unmarshall cassette json: %v", err)
				return false
			}
			return reflect.DeepEqual(reqJson, cassetteJson)
		}

		return true
	})

	rec.AddHook(func(i *cassette.Interaction) error {
		authHeader := "Authorization"
		if auth, ok := firstHeaderValue(authHeader, i.Request.Headers); ok {
			i.Request.Headers.Del(authHeader)
			parts := strings.Split(auth, " ")
			i.Request.Headers.Set("Authorization", fmt.Sprintf("%s REDACTED", parts[0]))
		}

		// save disk space, clean up what gets written to disk
		deleteResponseHeaders := []string{"duration", "Content-Security-Policy", "Cache-Control", "Expect-Ct", "Expires", "P3p", "Pragma", "Public-Key-Pins-Report-Only", "Server", "Set-Cookie", "Strict-Transport-Security", "Vary"}
		for _, header := range deleteResponseHeaders {
			i.Response.Headers.Del(header)
		}
		for name := range i.Response.Headers {
			// delete all X-headers
			if strings.HasPrefix(name, "X-") {
				i.Response.Headers.Del(name)
				continue
			}
		}

		// need to scrub OKTA_ORG_NAME+OKTA_BASE_URL strings and rewrite as
		// [cassette-name].oktapreview.com
		vcrHostname := fmt.Sprintf("%s.oktapreview.com", mgr.CurrentCassette)
		orgUrl, _ := url.Parse(i.Request.URL)
		i.Request.URL = strings.ReplaceAll(i.Request.URL, orgUrl.Host, vcrHostname)
		i.Response.Body = strings.ReplaceAll(i.Response.Body, orgUrl.Host, vcrHostname)
		headerLinks := replaceHeaderValues(i.Response.Headers["Link"], orgUrl.Host, vcrHostname)
		i.Response.Headers.Del("Link")
		for _, val := range headerLinks {
			i.Response.Headers.Add("Link", val)
		}

		return nil
	}, recorder.AfterCaptureHook)

	// VCR takes over http transport duties
	rt := http.RoundTripper(rec)
	config.resetHttpTransport(&rt)

	providerConfigsLock.Lock()
	providerConfigs[mgr.TestAndCassetteNameKey()] = config
	providerConfigsLock.Unlock()
	return config, nil
}

func replaceHeaderValues(currentValues []string, oldValue, newValue string) []string {
	result := []string{}
	for _, text := range currentValues {
		result = append(result, strings.ReplaceAll(text, oldValue, newValue))
	}
	return result
}

func firstHeaderValue(name string, headers http.Header) (string, bool) {
	if vals, ok := headers[name]; ok && len(vals) > 0 {
		return vals[0], true
	}
	return "", false
}

// closeRecorder closes the VCR recorder to save the cassette file
func closeRecorder(t *testing.T, vcr *vcrManager) {
	providerConfigsLock.RLock()
	config, ok := providerConfigs[vcr.TestAndCassetteNameKey()]
	providerConfigsLock.RUnlock()
	if ok {
		// don't record failing test runs
		if !t.Failed() {
			// If a test succeeds, write new seed/yaml to files
			err := config.oktaSDKClientV2.GetConfig().HttpClient.Transport.(*recorder.Recorder).Stop()
			if err != nil {
				t.Error(err)
			}
		}
		// Clean up test config
		providerConfigsLock.Lock()
		delete(providerConfigs, t.Name())
		providerConfigsLock.Unlock()
	}
}

// newVCRManager Returns a vcr manager
func newVCRManager(testName string) *vcrManager {
	dir, _ := os.Getwd()
	vcrFixturesHome := path.Join(dir, "../test/fixtures/vcr")
	cassettesPath := path.Join(vcrFixturesHome, testName)
	return &vcrManager{
		Name:            testName,
		FixturesHome:    vcrFixturesHome,
		CassettesPath:   cassettesPath,
		CurrentCassette: os.Getenv("OKTA_VCR_CASSETTE"),
		VCRModeName:     os.Getenv("OKTA_VCR_TF_ACC"),
	}
}

func (m *vcrManager) SetCurrentCassette(name string) {
	m.CurrentCassette = name
}

// TestAndCassetteNameKey is intended to be used as the key for configs that
// utilized for each test cassette.
func (m *vcrManager) TestAndCassetteNameKey() string {
	return fmt.Sprintf("%s-%s", m.Name, m.CurrentCassette)
}

// VcrEnabled test is considered to be in VCR mode if ENV var OKTA_VCR_TF_ACC
// is not empty. Valid values are "record" for recording VCR cassettes (test
// runs), and "play" for playing all cassettes of a test.
func (m *vcrManager) VCREnabled() bool {
	return m.VCRModeName != ""
}

// ValidMode Is this a valid vcr mode given vcr is enabled?
func (m *vcrManager) ValidMode() bool {
	return m.VCREnabled() && (m.VCRModeName == "play" || m.VCRModeName == "record")
}

// HasCassettesToPlay VCR is in play mode and there are cassette files to play.
func (m *vcrManager) HasCassettesToPlay() bool {
	cassetteDir := filepath.Dir(m.CassettesPath)
	files, err := os.ReadDir(cassetteDir)
	if err != nil {
		return false
	}
	return m.VCRModeName == "play" && len(files) > 0
}

// CassettePath the path to what would be the current cassette.
func (m *vcrManager) CassettePath() string {
	return path.Join(m.CassettesPath, m.CurrentCassette)
}

// AttemptedWriteOfExistingCassette given a vcr mode of record the current
// cassette file exists.
func (m *vcrManager) AttemptedWriteOfExistingCassette() bool {
	_, err := os.Stat(fmt.Sprintf("%s.yaml", m.CassettePath()))
	return m.VCRModeName == "record" && err == nil
}

// AttemptedWriteIsMissingCassetteName Tests if cassette name is present for recording
func (m *vcrManager) AttemptedWriteIsMissingCassetteName() bool {
	return m.VCRModeName == "record" && os.Getenv("OKTA_VCR_CASSETTE") == ""
}

// VCRMode the recorder.Mode value based on OKTA_VCR_TF_ACC
func (m *vcrManager) VCRMode() recorder.Mode {
	if m.VCRModeName == "record" {
		return recorder.ModeRecordOnly
	}

	return recorder.ModeReplayOnly
}

// IsPlaying VCR mode is play
func (m *vcrManager) IsPlaying() bool {
	return m.VCRModeName == "play"
}

// IsRecording VCR mode is record
func (m *vcrManager) IsRecording() bool {
	return m.VCRModeName == "record"
}

// Cassettes Slice of all cassettes for the given test.
func (m *vcrManager) Cassettes() []string {
	if os.Getenv("OKTA_VCR_CASSETTE") != "" {
		return []string{os.Getenv("OKTA_VCR_CASSETTE")}
	}

	cassettes := []string{}
	files, err := os.ReadDir(path.Join(m.CassettesPath))
	if err != nil {
		return cassettes
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".") {
			continue
		}
		parts := strings.Split(file.Name(), ".")
		cassettes = append(cassettes, parts[0])
	}
	return cassettes
}

type vcrManager struct {
	Name            string
	FixturesHome    string
	CassettesPath   string
	CurrentCassette string
	VCRModeName     string
}
