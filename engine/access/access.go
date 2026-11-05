// Package access is Taskiem's permissions and built-in roles (spec 13.3):
// the one table of which built-in role holds which permission. The API
// checks requests against it, and engine packages that decide who gets
// something (the money summaries' recipients, engine/moneydigest) read it
// here, so there is no second copy to keep equal.
//
// Any other membership role is either a custom role (a tenant's named set
// of these permissions, in the roles table) or a business role, which
// carries no permission and only qualifies its holder for approvals that
// name it.
package access

import "slices"

// Permissions.
const (
	WorkflowRead     = "workflow.read"
	WorkflowEdit     = "workflow.edit"
	WorkflowPublish  = "workflow.publish"
	RunRead          = "run.read"
	RunStart         = "run.start"
	RunCancel        = "run.cancel"
	RunResolve       = "run.resolve"
	ApprovalDecide   = "approval.decide"
	PIIReveal        = "pii.reveal"
	PIIErase         = "pii.erase"
	SecretManage     = "secret.manage"
	ConnectionManage = "connection.manage"
	MemberManage     = "member.manage"
	AuditRead        = "audit.read"
	GitManage        = "git.manage"
	PolicyManage     = "policy.manage"
	ConnectorManage  = "connector.manage"
	RoleManage       = "role.manage"
	SCIM             = "scim.provision" // an identity provider's key
	AlertManage      = "alert.manage"
	// Data tables (decision 0033): reading a table's rows, and creating
	// and changing tables and rows.
	TableRead = "table.read"
	TableEdit = "table.edit"
	// Storefronts and hosted forms (decisions 0036, 0037): the public
	// pages and the websites allowed to embed them.
	StorefrontManage = "storefront.manage"
	// FormLinks mints one-time links to a link form (forms 8.4).
	FormLinks = "form.links"
	// BillingManage manages the subscription (docs/billing.md).
	BillingManage = "billing.manage"
	// InvoiceRead lists and reads the business's invoices to its
	// customers (decision 0045).
	InvoiceRead = "invoice.read"
	// InvoiceManage creates, sends and voids them, changes their settings
	// and undoes a wrong payment.
	InvoiceManage = "invoice.manage"
	// KeyManage manages the tenant's encryption keys (docs/byok.md).
	KeyManage = "key.manage"
	// PartnerRead and PartnerManage are the partner API's (docs/partners.md).
	PartnerRead   = "partner.read"
	PartnerManage = "partner.manage"
	// The Schools area (decision 0046; docs/schools.md): seeing students,
	// fees and clearance; changing them; approving what needs a second
	// person; and the gate screen only.
	SchoolRead    = "school.read"
	SchoolManage  = "school.manage"
	SchoolApprove = "school.approve"
	SchoolGate    = "school.gate"
	// LinkManage requests, accepts, declines, blocks and ends links with
	// other businesses on Taskiem (decision 0061; docs/spec/business-links.md).
	LinkManage = "link.manage"
	// ChatTrade writes in the conversations with linked businesses (their
	// messages are readable by every member).
	ChatTrade = "chat.trade"
)

// All are every permission, in the order the role editor lists them.
var All = []string{WorkflowRead, WorkflowEdit, WorkflowPublish, RunRead, RunStart, RunCancel,
	RunResolve, ApprovalDecide, PIIReveal, PIIErase, SecretManage, ConnectionManage, MemberManage, AuditRead, GitManage, PolicyManage, ConnectorManage, RoleManage, SCIM, AlertManage,
	TableRead, TableEdit, StorefrontManage, FormLinks,
	BillingManage, InvoiceRead, InvoiceManage, KeyManage, PartnerRead, PartnerManage,
	SchoolRead, SchoolManage, SchoolApprove, SchoolGate, LinkManage, ChatTrade}

// Descriptions say what each permission lets a person do, for the role
// editor.
var Descriptions = map[string]string{
	WorkflowRead:     "See workflows and their versions",
	WorkflowEdit:     "Create and edit workflow drafts",
	WorkflowPublish:  "Publish workflow versions",
	RunRead:          "See runs and their history",
	RunStart:         "Start runs",
	RunCancel:        "Cancel runs",
	RunResolve:       "Resolve steps that need reconciliation",
	ApprovalDecide:   "Decide approvals for roles held",
	PIIReveal:        "Reveal sealed personal data (audited)",
	PIIErase:         "Erase a person's data",
	SecretManage:     "Manage secrets, variables and egress",
	ConnectionManage: "Manage connections and acknowledge contract drift",
	MemberManage:     "Manage members and API keys",
	AuditRead:        "Read the audit log and reports",
	GitManage:        "Connect environments to Git",
	PolicyManage:     "Manage approval policies",
	ConnectorManage:  "Upload the tenant's own connectors",
	RoleManage:       "Manage custom roles",
	AlertManage:      "Manage alert channels and rules",
	StorefrontManage: "Manage the storefront, hosted forms and the websites allowed to embed them",
	SCIM:             "Provision members over SCIM (for an identity provider's key)",
	BillingManage:    "Manage the subscription: plans, payments, invoices and cancellation",
	InvoiceRead:      "See the invoices sent to customers",
	InvoiceManage:    "Create, send and void invoices to customers, and change their settings",
	KeyManage:        "Manage encryption keys: rotate the tenant key, bring your own key (customer KMS), check and replace its credentials",
	PartnerRead:      "Partner API: see sub-tenants, their workflows, runs and usage, embed apps and webhook deliveries (audited in both tenants)",
	PartnerManage:    "Partner API: create and suspend sub-tenants, invite their people, install workflow packages, manage embed apps, mint and revoke end-user tokens",
	SchoolRead:       "Schools: see students, families, classes and terms, fees, payments, debtors and clearance",
	SchoolManage:     "Schools: add and change students, families, classes, sessions and terms; import students; promote classes",
	SchoolApprove:    "Schools: approve cash postings and discounts above the thresholds, decide payment plans, override clearance",
	SchoolGate:       "Schools: the gate screen only (cleared or see the bursar, never an amount)",
	LinkManage:       "Link with other businesses on Taskiem: ask, accept, decline, block and end links (audited in both businesses)",
	ChatTrade:        "Write to linked businesses in their conversations, as the business",
}

// Gate is the built-in role of the person at the school gate: the gate
// screen and nothing else (docs/spec/schools.md 12.3).
const Gate = "gate"

// Roles are the built-in roles and their permissions. The owner holds
// every permission.
var Roles = map[string][]string{
	"owner": All,
	"admin": {WorkflowRead, WorkflowEdit, WorkflowPublish, RunRead, RunStart, RunCancel, RunResolve, SecretManage, ConnectionManage, MemberManage, AuditRead, GitManage, PolicyManage,
		ConnectorManage, RoleManage, SCIM, AlertManage, TableRead, TableEdit, StorefrontManage, FormLinks, InvoiceRead, InvoiceManage, SchoolRead, SchoolManage, LinkManage, ChatTrade},
	"builder":  {WorkflowRead, WorkflowEdit, RunRead, RunStart, TableRead, TableEdit},
	"operator": {WorkflowRead, RunRead, RunStart, RunCancel, RunResolve, TableRead, InvoiceRead, InvoiceManage, SchoolRead, SchoolManage, ChatTrade},
	"approver": {WorkflowRead, RunRead, ApprovalDecide},
	"auditor":  {WorkflowRead, RunRead, AuditRead, TableRead, InvoiceRead, SchoolRead},
	"viewer":   {WorkflowRead, RunRead, TableRead, InvoiceRead, SchoolRead},
	Gate:       {SchoolGate},
}

// Builtin reports whether role is a built-in role.
func Builtin(role string) bool {
	_, ok := Roles[role]
	return ok
}

// Holding are the built-in roles holding perm, sorted.
func Holding(perm string) []string {
	var out []string
	for r, perms := range Roles {
		if slices.Contains(perms, perm) {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

// BuiltinNames are the built-in roles' names, sorted.
func BuiltinNames() []string {
	out := make([]string, 0, len(Roles))
	for r := range Roles {
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}

// GateOnly reports whether a set of permissions is a gate session's: the
// gate screen and nothing else. The server refuses such a session every
// route but signing out and reading who it is.
func GateOnly(perms map[string]bool) bool {
	if !perms[SchoolGate] {
		return false
	}
	for p, ok := range perms {
		if ok && p != SchoolGate {
			return false
		}
	}
	return true
}
