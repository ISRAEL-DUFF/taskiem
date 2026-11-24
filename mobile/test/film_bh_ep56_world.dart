// The stand-in server for the Bright Hill Academy films of Episodes 5 and 6
// (school-invoice-term, school-get-paid). Copied from school_fixture.dart
// (schoolServer) with the story's own numbers: about 400 students, JSS 1A's
// fees Tuition 85,000 + PTA levy 5,000 + Books 12,000 = 102,000, Transport
// 20,000 optional. Anything not answered here falls to schoolServer.

import 'dart:convert';

import 'package:http/http.dart' as http;

import 'school_fixture.dart';

http.Response _j(Object b, [int s = 200]) => http.Response(jsonEncode(b), s, headers: {'content-type': 'application/json'});

/// What the sheets have sent (the tests read it).
final bhSent = <Map<String, dynamic>>[];

const _classTotals = [('JSS 1A', 90, 9180000), ('JSS 1B', 88, 8976000), ('JSS 2A', 76, 8360000), ('Primary 4', 70, 6440000), ('SS 1B', 76, 9844000)];

/// The School tab before the term is invoiced (Episode 5), or at 70% (Episode 6).
Map<String, dynamic> bhOverview({required bool invoiced}) => {
  'overview': {
    'term': 'First term · 2026/2027', 'term_state': 'current', 'enrolled': 400, 'invoiced': invoiced ? 396 : 0,
    'expected_kobo': invoiced ? 4280000000 : 0, 'expected_by_now_kobo': invoiced ? 4280000000 : 0, 'collected_kobo': invoiced ? 2996000000 : 0,
    'outstanding_kobo': invoiced ? 1284000000 : 0, 'overdue_kobo': 0, 'collected_percent': invoiced ? 70 : 0, 'this_week_kobo': invoiced ? 231000000 : 0,
    'pass_through_kobo': 0, 'pass_through': [],
    'matched': {'auto_kobo': 1, 'person_kobo': 1, 'auto_percent': invoiced ? 86 : 0},
    'how_paid': invoiced
        ? [{'how': 'account', 'payments': 300, 'kobo': 74}, {'how': 'transfer', 'payments': 30, 'kobo': 14}, {'how': 'cash', 'payments': 20, 'kobo': 9}, {'how': 'card', 'payments': 4, 'kobo': 3}]
        : [],
    'top_debtors': [],
    'aging': {'not_due': {'students': 0}, '0_7': {'students': 0}, '7_14': {'students': 0}, '14_30': {'students': 0}, 'over_30': {'students': 0}},
    'debtors': invoiced ? 120 : 0, 'debtors_kobo': invoiced ? 1284000000 : 0,
    'last_close': null,
  },
};

Map<String, dynamic> _student(String id, String adm, String name, String cls, String classId, String guardian, String phone, String rel) => {
  'id': id, 'admission_number': adm, 'name': name, 'class_id': classId, 'class': cls, 'status': 'active', 'family_id': 'F$id',
  'family': {'id': 'F$id', 'guardians': [{'id': 'G$id', 'name': guardian, 'phone': phone, 'relationship': rel}], 'students': [{'id': id, 'admission_number': adm, 'name': name, 'class': cls, 'status': 'active'}]},
  'enrollments': [{'term': 'First term', 'session': '2026/2027', 'term_state': 'current', 'class': cls}],
};

final bhTobi = _student('T1', 'JSS1A/022', 'Tobi Lawal', 'JSS 1A', 'c0', 'Bimpe Lawal', '0803 ••• 1187', 'Mother');
final bhDayo = _student('D1', 'P4/027', 'Dayo Danjuma', 'Primary 4', 'c3', 'Segun Danjuma', '0805 ••• 3350', 'Father');

Map<String, dynamic> _fees(Map<String, dynamic> inv, List<(String, int)> lines) => {
  'term_id': 'T1', 'items': [{'id': 'bus', 'name': 'Transport', 'amount_kobo': 2000000, 'kind': 'optional'}], 'chosen': [],
  'lines': [for (final (d, k) in lines) {'description': d, 'quantity': 1, 'unit_kobo': k}],
  'total_kobo': inv['total_kobo'], 'invoice': inv,
};

/// Episode 5: the invoice plan (400 enrolled), what the sheet sends.
/// Episode 6: the two students, their accounts, cash needing a second person.
Future<http.Response?> bhServer(http.Request req, {bool invoiced = false}) async {
  final p = req.url.path;
  final m = req.method;
  if (m == 'POST' && p.endsWith('/invoice')) {
    final body = req.body.isEmpty ? const <String, dynamic>{} : jsonDecode(req.body) as Map<String, dynamic>;
    if (body.containsKey('confirm')) {
      bhSent.add(body);
      return _j({'created': 396, 'sent': body['send'] == true ? 391 : 0, 'requested': 0});
    }
    return _j({'plan': {
      'enrolled': 400, 'to_invoice': 396, 'total_kobo': 4280000000, 'pass_through_kobo': 0, 'already_invoiced': 0, 'hash': 'h',
      'classes': [for (final (c, n, k) in _classTotals) {'class': c, 'students': n, 'total_kobo': k * 100}],
      'no_contact': [{'student_id': 'N1', 'admission_number': 'JSS1B/030', 'name': ''}, {'student_id': 'N3', 'admission_number': 'P4/040', 'name': ''}],
      'needs_approval': [{'student_id': 'N2', 'admission_number': 'P4/011', 'name': 'Funke Ajayi'}], 'no_fees': [], 'nothing_to_pay': [],
    }});
  }
  if (m == 'POST' && p == '/v1/schools/payments') {
    bhSent.add(jsonDecode(req.body) as Map<String, dynamic>);
    return _j({'code': 'needs_approval'}, 202);
  }
  if (m != 'GET') return null;
  if (p == '/v1/schools/overview') return _j(bhOverview(invoiced: invoiced));
  if (p == '/v1/schools/payments') return _j({'postings': []});
  if (p == '/v1/schools/classes') {
    return _j({'classes': [for (final (i, (c, n, _)) in _classTotals.indexed) {'id': 'c$i', 'name': c, 'label': c, 'order': i, 'students': n}]});
  }
  if (p == '/v1/schools/sessions') {
    return _j({'sessions': [{'id': 'SE', 'label': '2026/2027', 'terms': [
      {'id': 'T1', 'name': 'First term', 'starts_on': '2026-09-07', 'ends_on': '2026-12-18', 'fees_due_on': '2026-09-21', 'state': 'current', 'enrolled': 400},
      {'id': 'T2', 'name': 'Second term', 'starts_on': '2027-01-11', 'ends_on': '2027-04-09', 'fees_due_on': '2027-01-25', 'state': 'planned', 'enrolled': 0},
    ]}]});
  }
  if (p == '/v1/schools/students') {
    final q = (req.url.queryParameters['q'] ?? '').toLowerCase();
    final all = [
      for (final (n, a, c, id) in [
        ('Amaka Obi', 'JSS1A/014', 'JSS 1A', 'S1'), ('Bolu Ade', 'JSS1A/021', 'JSS 1A', 'S2'), ('Dayo Danjuma', 'P4/027', 'Primary 4', 'D1'),
        ('Funke Ajayi', 'P4/011', 'Primary 4', 'S5'), ('Tobi Lawal', 'JSS1A/022', 'JSS 1A', 'T1'),
      ])
        {'id': id, 'name': n, 'admission_number': a, 'class': c, 'status': 'active'},
    ].where((s) => q.isEmpty || '${s['name']}'.toLowerCase().contains(q) || '${s['admission_number']}'.toLowerCase().contains(q)).toList();
    return _j({'total': q.isEmpty ? 400 : all.length, 'students': all});
  }
  for (final (id, s, inv, lines, acct) in [
    ('T1', bhTobi, <String, dynamic>{'invoice_id': 'I1', 'number': 'INV-0022', 'status': 'unpaid', 'term': 'First term', 'total_kobo': 10200000, 'paid_kobo': 0, 'due_kobo': 10200000},
      [('Tuition', 8500000), ('PTA levy', 500000), ('Books', 1200000)], ('9012345678', 'Bright Hill Academy/Tobi Lawal')),
    ('D1', bhDayo, <String, dynamic>{'invoice_id': 'I2', 'number': 'INV-0310', 'status': 'unpaid', 'term': 'First term', 'total_kobo': 9200000, 'paid_kobo': 0, 'due_kobo': 9200000},
      [('Tuition', 7500000), ('PTA levy', 500000), ('Books', 1200000)], ('9087654321', 'Bright Hill Academy/Dayo Danjuma')),
  ]) {
    final base = '/v1/schools/students/$id';
    if (p == base) return _j({'student': s});
    if (p == '$base/chasing') return _j({'balance_kobo': inv['due_kobo'], 'days_past_due': 0, 'bucket': 'not_due', 'promises': [], 'reminders': []});
    if (p == '$base/fees') return _j(_fees(inv, lines));
    if (p == '$base/accounts') return _j({'accounts': [{'status': 'issued', 'account_number': acct.$1, 'bank_name': 'Wema Bank', 'account_name': acct.$2}]});
    if (p == '$base/clearance') return _j({'clearance': {'status': 'not_paid', 'reason': 'balance', 'balance_kobo': inv['due_kobo']}, 'overrides': []});
    if (p == '$base/discounts') return _j({'discounts': []});
  }
  return schoolServer(req);
}

/// Mr Ibe's cash for Dayo (Mr Danjuma's N60,000), as the chat card, for Mrs Fashola.
const bhApprovalAlert = {
  'key': 'school_approval:P7', 'kind': 'school_approval', 'at': '2026-11-11T12:12:00Z', 'title': 'Cash at the desk', 'line': 'P4/027 · Primary 4 · asked by bursar@brighthill.example',
  'amount_kobo': 6000000, 'id': 'P7', 'who': 'Mr Ibe',
  'school': {'id': 'P7', 'kind': 'cash_posting', 'status': 'pending', 'admission_number': 'P4/027', 'name': 'D•••', 'class': 'Primary 4', 'invoice_number': 'INV-0310',
    'amount_kobo': 6000000, 'details': {'how': 'cash', 'date': '2026-11-11'}, 'requested_by': 'user:x', 'requested_email': 'bursar@brighthill.example', 'requested_at': '2026-11-11T12:12:00Z'},
};
