package okta

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ provider.Provider = &FrameworkProvider{}
)

// NewFrameworkProvider is a helper function to simplify provider server and
// testing implementation.
func NewFrameworkProvider(version string) provider.Provider {
	return &FrameworkProvider{
		Config:  Config{},
		Version: version,
	}
}

type FrameworkProvider struct {
	Config
	Version string
}

type FrameworkProviderData struct {
	OrgName        types.String `tfsdk:"org_name"`
	AccessToken    types.String `tfsdk:"access_token"`
	APIToken       types.String `tfsdk:"api_token"`
	ClientID       types.String `tfsdk:"client_id"`
	Scopes         types.Set    `tfsdk:"scopes"`
	PrivateKey     types.String `tfsdk:"private_key"`
	PrivateKeyID   types.String `tfsdk:"private_key_id"`
	BaseURL        types.String `tfsdk:"base_url"`
	HTTPProxy      types.String `tfsdk:"http_proxy"`
	Backoff        types.Bool   `tfsdk:"backoff"`
	MinWaitSeconds types.Int64  `tfsdk:"min_wait_seconds"`
	MaxWaitSeconds types.Int64  `tfsdk:"max_wait_seconds"`
	MaxRetries     types.Int64  `tfsdk:"max_retries"`
	Parallelism    types.Int64  `tfsdk:"parallelism"`
	LogLevel       types.Int64  `tfsdk:"log_level"`
	MaxAPICapacity types.Int64  `tfsdk:"max_api_capacity"`
	RequestTimeout types.Int64  `tfsdk:"request_timeout"`
}

// Metadata returns the provider type name.
func (p *FrameworkProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "okta"
	resp.Version = p.Version
}

// Schema defines the provider-level schema for configuration data.
func (p *FrameworkProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"org_name": schema.StringAttribute{
				Optional:    true,
				Description: "The organization to manage in Okta.",
			},
			"access_token": schema.StringAttribute{
				Optional:    true,
				Description: "Bearer token granting privileges to Okta API.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.Expressions{
						path.MatchRoot("api_token"),
						path.MatchRoot("client_id"),
						path.MatchRoot("scopes"),
						path.MatchRoot("private_key"),
					}...),
				},
			},
			"api_token": schema.StringAttribute{
				Optional:    true,
				Description: "API Token granting privileges to Okta API.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.Expressions{
						path.MatchRoot("access_token"),
						path.MatchRoot("client_id"),
						path.MatchRoot("scopes"),
						path.MatchRoot("private_key"),
					}...),
				},
			},
			"client_id": schema.StringAttribute{
				Optional:    true,
				Description: "API Token granting privileges to Okta API.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.Expressions{
						path.MatchRoot("access_token"),
						path.MatchRoot("api_token"),
					}...),
				},
			},
			"scopes": schema.SetAttribute{
				Optional:    true,
				Description: "API Token granting privileges to Okta API.",
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.ConflictsWith(path.Expressions{
						path.MatchRoot("access_token"),
						path.MatchRoot("api_token"),
					}...),
				},
			},
			"private_key": schema.StringAttribute{
				Optional:    true,
				Description: "API Token granting privileges to Okta API.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.Expressions{
						path.MatchRoot("access_token"),
						path.MatchRoot("api_token"),
					}...),
				},
			},
			"private_key_id": schema.StringAttribute{
				Optional:    true,
				Description: "API Token Id granting privileges to Okta API.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.Expressions{
						path.MatchRoot("api_token"),
					}...),
				},
			},
			"base_url": schema.StringAttribute{
				Optional:    true,
				Description: "The Okta url. (Use 'oktapreview.com' for Okta testing)",
			},
			"http_proxy": schema.StringAttribute{
				Optional:    true,
				Description: "Alternate HTTP proxy of scheme://hostname or scheme://hostname:port format",
			},
			"backoff": schema.BoolAttribute{
				Optional:    true,
				Description: "Use exponential back off strategy for rate limits.",
			},
			"min_wait_seconds": schema.Int64Attribute{
				Optional:    true,
				Description: "minimum seconds to wait when rate limit is hit. We use exponential backoffs when backoff is enabled.",
			},
			"max_wait_seconds": schema.Int64Attribute{
				Optional:    true,
				Description: "maximum seconds to wait when rate limit is hit. We use exponential backoffs when backoff is enabled.",
			},
			"max_retries": schema.Int64Attribute{
				Optional:    true,
				Description: "maximum number of retries to attempt before erroring out.",
				Validators: []validator.Int64{
					int64validator.AtMost(100),
				},
			},
			"parallelism": schema.Int64Attribute{
				Optional:    true,
				Description: "Number of concurrent requests to make within a resource where bulk operations are not possible. Take note of https://developer.okta.com/docs/api/getting_started/rate-limits.",
			},
			"log_level": schema.Int64Attribute{
				Optional:    true,
				Description: "providers log level. Minimum is 1 (TRACE), and maximum is 5 (ERROR)",
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
					int64validator.AtMost(5),
				},
			},
			"max_api_capacity": schema.Int64Attribute{
				Optional: true,
				Description: "(Experimental) sets what percentage of capacity the provider can use of the total rate limit " +
					"capacity while making calls to the Okta management API endpoints. Okta API operates in one minute buckets. " +
					"See Okta Management API Rate Limits: https://developer.okta.com/docs/reference/rl-global-mgmt/",
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
					int64validator.AtMost(100),
				},
			},
			"request_timeout": schema.Int64Attribute{
				Optional:    true,
				Description: "Timeout for single request (in seconds) which is made to Okta, the default is `0` (means no limit is set). The maximum value can be `300`.",
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
					int64validator.AtMost(300),
				},
			},
		},
	}
}

func (p *FrameworkProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// Retrieve provider data from configuration
	var data FrameworkProviderData
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := p.handleFrameworkDefaults(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("failed to load default value to provider", err.Error())
		return
	}

	p.orgName = data.OrgName.ValueString()
	p.accessToken = data.AccessToken.ValueString()
	p.apiToken = data.APIToken.ValueString()
	p.clientID = data.ClientID.ValueString()
	p.privateKey = data.PrivateKey.ValueString()
	p.privateKeyId = data.PrivateKeyID.ValueString()
	p.domain = data.BaseURL.ValueString()
	p.maxAPICapacity = int(data.MaxWaitSeconds.ValueInt64())
	p.backoff = data.Backoff.ValueBool()
	p.minWait = int(data.MinWaitSeconds.ValueInt64())
	p.maxWait = int(data.MaxRetries.ValueInt64())
	p.retryCount = int(data.MaxRetries.ValueInt64())
	p.parallelism = int(data.Parallelism.ValueInt64())
	p.logLevel = int(data.LogLevel.ValueInt64())
	p.requestTimeout = int(data.RequestTimeout.ValueInt64())
	for _, val := range data.Scopes.Elements() {
		var v types.String
		tfsdk.ValueAs(ctx, val, &v)
		p.scopes = append(p.scopes, v.ValueString())
	}

	if !data.HTTPProxy.IsNull() {
		p.httpProxy = data.HTTPProxy.ValueString()
	}

	if err := p.loadClients(ctx); err != nil {
		resp.Diagnostics.AddError("failed to load default value to provider", err.Error())
		return
	}
	p.SetTimeOperations(NewProductionTimeOperations())

	resp.DataSourceData = &p.Config
	resp.ResourceData = &p.Config
}

// DataSources defines the data sources implemented in the provider.
func (p *FrameworkProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewOrgMetadataDataSource,
		NewDefaultSigninPageDataSource,
		NewLogStreamDataSource,
	}
}

// Resources defines the resources implemented in the provider.
func (p *FrameworkProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewAppAccessPolicyAssignmentResource,
		NewAppOAuthRoleAssignmentResource,
		NewBrandResource,
		NewLogStreamResource,
		NewPolicyDeviceAssuranceAndroidResource,
		NewPolicyDeviceAssuranceChromeOSResource,
		NewPolicyDeviceAssuranceIOSResource,
		NewPolicyDeviceAssuranceMacOSResource,
		NewPolicyDeviceAssuranceWindowsResource,
		NewCustomizedSigninResource,
		NewPreviewSigninResource,
	}
}

func dataSourceConfiguration(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *Config {
	if req.ProviderData == nil {
		return nil
	}

	config, ok := req.ProviderData.(*Config)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *Config, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return nil
	}

	return config
}

func resourceConfiguration(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *Config {
	if req.ProviderData == nil {
		return nil
	}

	p, ok := req.ProviderData.(*Config)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Config, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return nil
	}

	return p
}
