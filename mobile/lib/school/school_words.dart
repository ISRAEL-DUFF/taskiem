// The school's words and small helpers (docs/schools.md; the web's
// lib/schoolChasing.ts, lib/schoolClearance.ts): how late, how paid, why
// a child is or is not cleared, the reminder rungs.

import '../app/auth.dart';

List<Map<String, dynamic>> maps(Object? v) => (v as List? ?? const []).whereType<Map>().map((m) => m.cast<String, dynamic>()).toList();
Map<String, dynamic>? mapOf(Object? v) => v is Map ? v.cast<String, dynamic>() : null;
int kobo(Object? v) => (v as num?)?.toInt() ?? 0;
String str(Object? v) => v is String ? v : (v == null ? '' : '$v');

/// The school's paths on the server.
const schoolApi = '/v1/schools';

/// What may be read, done, approved; names shown (audited) when they may be.
extension SchoolCan on Auth {
  bool get schoolRead => can('school.read');
  bool get schoolManage => can('school.manage');
  bool get schoolApprove => can('school.approve');
  bool get mayReveal => can('pii.reveal');

  /// `?reveal=true` (or `&reveal=true`) when names may be shown.
  String reveal([String join = '?']) => mayReveal ? '${join}reveal=true' : '';
}

/// Aging buckets (engine/schools/chasing.go Buckets), in order.
const buckets = ['not_due', '0_7', '7_14', '14_30', 'over_30'];
const bucketWords = {'not_due': 'Not yet due', '0_7': '0 to 7 days', '7_14': '7 to 14 days', '14_30': '14 to 30 days', 'over_30': 'Over 30 days'};

/// How parents paid (close.go PaidByWords), short.
const howPaidWords = {'account': 'Into accounts', 'card': 'Card or POS', 'cash': 'Cash', 'transfer': 'Transfer', 'other': 'Other'};

/// How a payment at the desk was made (cash.go).
const deskWays = [('cash', 'Cash'), ('transfer', 'Transfer'), ('pos', 'POS'), ('other', 'Other')];
String deskWay(String how) => {for (final (k, w) in deskWays) k: w}[how] ?? how;

/// Clearance (clearance.go): the status and why.
const clearanceWords = {'cleared': 'Cleared', 'partly_paid': 'Partly paid', 'not_paid': 'Not paid'};
const clearanceReasons = {
  'paid': 'Paid in full',
  'plan': 'Payment plan in good standing',
  'override': 'Cleared by override',
  'no_fees': 'Nothing to pay',
  'balance': 'A balance is left',
  'no_invoice': 'Not invoiced yet',
};

/// The reminder ladder (web lib/schoolChasing.ts RUNGS).
const rungs = [
  (n: 1, name: 'Gentle', says: 'A reminder of the balance and the account number.'),
  (n: 2, name: 'Firmer', says: 'The balance, and that the due date has passed.'),
  (n: 3, name: 'Final', says: 'The balance, and what happens next.'),
];
String rungName(int n) => rungs.where((r) => r.n == n).map((r) => r.name).firstOrNull ?? '$n';

/// The rung a family is due, by how late (≤7 gentle, ≤14 firmer, else final).
int rungFor(int daysPastDue) => daysPastDue <= 7 ? 1 : (daysPastDue <= 14 ? 2 : 3);

/// "41 days late", "due 21 Sep", "not yet due".
String lateWords(int days, {String dueOn = ''}) {
  if (days > 0) return days == 1 ? '1 day late' : '$days days late';
  if (dueOn.isNotEmpty) return 'due ${dayWords(dueOn)}';
  return 'not yet due';
}

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const _days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

/// "Fri 20 Nov" from 2026-11-20 (or a time); the string itself if not a day.
String dayWords(String day, {bool weekday = true}) {
  final d = DateTime.tryParse(day);
  if (d == null) return day;
  final l = day.length > 10 ? d.toUtc().add(const Duration(hours: 1)) : d;
  return '${weekday ? '${_days[l.weekday - 1]} ' : ''}${l.day} ${_months[l.month - 1]}';
}

/// "15:40" in Lagos from a time.
String lagosClock(String at) {
  final d = DateTime.tryParse(at);
  if (d == null) return '';
  final l = d.toUtc().add(const Duration(hours: 1));
  return '${'${l.hour}'.padLeft(2, '0')}:${'${l.minute}'.padLeft(2, '0')}';
}

/// "YYYY-MM-DD" for a day.
String dayKey(DateTime d) => '${d.year}-${'${d.month}'.padLeft(2, '0')}-${'${d.day}'.padLeft(2, '0')}';

/// A student's line: admission number and class.
String studentLine(Map<String, dynamic> s) => [str(s['admission_number']), str(s['class'])].where((x) => x.isNotEmpty).join(' · ');

/// Whom a person is, by name where shown, else the admission number.
String nameOr(Map<String, dynamic> s) {
  final n = str(s['name']);
  return n.isEmpty || n.contains('•') ? str(s['admission_number']) : n;
}
