package peoplesafety

import "time"

// Groups' safety (docs/spec/people-chatting.md §8.2; people-chatting-5b.md
// B; "As built: stage 5b, group safety (server)"): how many groups a
// person may make, how many people they have not added as contacts they
// may invite to groups, and how many groups they may join by a link,
// weighed by the same signals as their requests.

// GroupLimits is what a person may do with groups in an hour and a day.
type GroupLimits struct {
	// CreatePerHour and CreatePerDay: groups they make.
	CreatePerHour, CreatePerDay int
	// StrangersPerDay: people they invite to a group who are not their
	// contacts (an admin adds only their contacts; anyone else gets an
	// invite, §9).
	StrangersPerDay int
	// LinkJoinsPerHour: groups they join (or ask to join) by a link.
	LinkJoinsPerHour int
}

// GroupAllowance is a person's group limits. They grow with the account's
// age (a week, 30 days, 90 days), and shrink when several people reported
// them, when their device has several accounts in their first week, and to
// the least while a moderator's limit is in force.
func GroupAllowance(s Signals) GroupLimits {
	var l GroupLimits
	switch {
	case s.FirstWeek():
		l = GroupLimits{CreatePerHour: 2, CreatePerDay: 3, StrangersPerDay: 10, LinkJoinsPerHour: 3}
	case s.Age < 30*24*time.Hour:
		l = GroupLimits{CreatePerHour: 3, CreatePerDay: 6, StrangersPerDay: 30, LinkJoinsPerHour: 5}
	case s.Age < 90*24*time.Hour:
		l = GroupLimits{CreatePerHour: 5, CreatePerDay: 10, StrangersPerDay: 60, LinkJoinsPerHour: 8}
	default:
		l = GroupLimits{CreatePerHour: 5, CreatePerDay: 20, StrangersPerDay: 100, LinkJoinsPerHour: 10}
	}
	third := func(n int) int { return max(1, n/3) }
	switch {
	case s.ReportedBy >= 5:
		l = GroupLimits{CreatePerHour: 1, CreatePerDay: 1, StrangersPerDay: 2, LinkJoinsPerHour: 1}
	case s.ReportedBy >= 2:
		l = GroupLimits{third(l.CreatePerHour), third(l.CreatePerDay), third(l.StrangersPerDay), third(l.LinkJoinsPerHour)}
	}
	if s.SharedDevice() {
		l = GroupLimits{CreatePerHour: 1, CreatePerDay: 1, StrangersPerDay: min(l.StrangersPerDay, 3), LinkJoinsPerHour: 1}
	}
	if s.Limited {
		l = GroupLimits{CreatePerHour: 1, CreatePerDay: 1, StrangersPerDay: min(l.StrangersPerDay, 2), LinkJoinsPerHour: min(l.LinkJoinsPerHour, 2)}
	}
	return l
}

// Group moderation's actions: turning off a group's invite link (admins
// can't make a new one while it is in force), and closing the group (no
// one writes in it, and it is deleted, §14, AppealWindow later unless its
// admins' appeal is upheld).
const (
	ActionGroupLinkOff = "group_link_off"
	ActionGroupClose   = "group_close"
)

// Scam checks in groups (5b B1): a message is checked for the members who
// have not added its sender, while the group is younger than
// GroupCheckYoung or the sender joined it by a link within
// GroupCheckJoined.
const (
	GroupCheckYoung  = 7 * 24 * time.Hour
	GroupCheckJoined = 24 * time.Hour
)

// A report of a member from inside a group weighs in the member's
// ReportedBy only when its reporter's account is past its first week, they
// had been in the group GroupReportAfter or more when reporting, and the
// member wrote within what the reporter sees; and then half: a reporter
// whose reports of the person all came from groups counts half a person
// (two such reporters make one). One-to-one reports count whole, as
// before. (5b B, the review's fixes.)
const GroupReportAfter = 24 * time.Hour
