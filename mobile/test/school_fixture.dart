// A stand-in for the school's half of the server, answering as drawn in the
// school screens' drawings (Greenfield School, First term 2026/2027).

import 'dart:convert';

import 'package:http/http.dart' as http;

http.Response _j(Object b, [int s = 200]) => http.Response(jsonEncode(b), s, headers: {'content-type': 'application/json'});

Map<String, dynamic> schoolDebtor(String id, String n, String c, int days, int bal, String b, {Map<String, dynamic>? extra}) =>
    {'student_id': id, 'admission_number': id, 'name': n, 'status': 'active', 'class_id': 'c', 'class': c, 'balance_kobo': bal, 'days_past_due': days, 'bucket': b, 'invoices': [], ...?extra};

final schoolClasses = [for (final (i, c) in ['JSS 1A', 'JSS 1B', 'JSS 2A', 'Primary 4', 'SS 1B'].indexed) {'id': 'c$i', 'name': c, 'label': c, 'order': i, 'students': [32, 30, 29, 27, 25][i]}];
final schoolAmaka = {'id': 'S1', 'admission_number': 'JSS1A/014', 'name': 'Amaka Obi', 'class_id': 'c0', 'class': 'JSS 1A', 'status': 'active', 'family_id': 'F1',
  'family': {'id': 'F1', 'guardians': [{'id': 'G1', 'name': 'Ngozi Obi', 'phone': '0803 ••• 4412', 'relationship': 'Mother'}],
    'students': [{'id': 'S1', 'admission_number': 'JSS1A/014', 'name': 'Amaka Obi', 'class': 'JSS 1A', 'status': 'active'}, {'id': 'S9', 'admission_number': 'SS1B/019', 'name': 'Ifeanyi Obi', 'class': 'SS 1B', 'status': 'active'}]},
  'enrollments': [{'term': 'First term', 'session': '2026/2027', 'term_state': 'current', 'class': 'JSS 1A'}, {'term': 'Third term', 'session': '2025/2026', 'term_state': 'past', 'class': 'Primary 6B'}]};

Future<http.Response?> schoolServer(http.Request req) async {
  final p = req.url.path;
  final m = req.method;
  if (m == 'POST' && p == '/v1/schools/accounts/issue') {
    return _j({'plan': {'issuer': {'connection': 'iswallet (main)', 'rail': 'iswallet', 'requirement': '', 'fee': '1.4%, at most ₦2,000'}, 'active': 418, 'already_issued': 402, 'to_issue': 14, 'later': 0,
      'waiting': [{'student_id': 'W1', 'admission_number': 'JSS2A/003', 'status': 'pending'}, {'student_id': 'W2', 'admission_number': 'P4/019', 'status': 'pending'}],
      'cannot': [{'student_id': 'C1', 'admission_number': 'JSS1B/030', 'reason': 'No guardian name'}, {'student_id': 'C2', 'admission_number': 'P4/040', 'reason': 'Name longer than the rail allows'}], 'hash': 'h'}});
  }
  if (m == 'POST' && p.endsWith('/invoice')) {
    return _j({'plan': {'enrolled': 418, 'to_invoice': 6, 'total_kobo': 114000000, 'pass_through_kobo': 9000000, 'already_invoiced': 412, 'hash': 'h',
      'classes': [{'class': 'JSS 1A', 'students': 2, 'total_kobo': 38000000}, {'class': 'Primary 4', 'students': 3, 'total_kobo': 57000000}, {'class': 'SS 1B', 'students': 1, 'total_kobo': 19000000}],
      'no_contact': [{'student_id': 'N1', 'admission_number': 'JSS1B/030', 'name': ''}], 'needs_approval': [{'student_id': 'N2', 'admission_number': 'P4/011', 'name': 'Funke Ajayi'}], 'no_fees': [], 'nothing_to_pay': []}});
  }
  if (m == 'POST' && p == '/v1/schools/students/import') {
    return _j({'hash': 'h', 'added': 12, 'updated': 406, 'saved': false, 'students': List.generate(418, (i) => {'row': i + 2}),
      'families': [
        {'index': 0, 'existing': true, 'guardians': [{'name': 'Ngozi Obi', 'phone': '0803'}], 'students': ['Amaka', 'Ifeanyi']},
        {'index': 1, 'existing': false, 'guardians': [{'name': 'Shola Adewale', 'phone': '0805'}], 'students': ['Tobi']},
      ]});
  }
  if (m != 'GET') return _j({});
  if (p == '/v1/schools/accounts/jobs') return _j({'jobs': []});
  if (p.endsWith('/promotion')) {
    return _j({'session': '2026/2027', 'done': false, 'promoted_at': null,
      'classes': [{'class': 'JSS 1A', 'next': 'JSS 2A', 'students': 32}, {'class': 'JSS 3A', 'next': 'SS 1A', 'students': 28}, {'class': 'SS 3A', 'next': '', 'students': 24}],
      'students': [
        {'id': 'S2', 'admission_number': 'JSS1A/021', 'name': 'Bolu Ade', 'class': 'JSS 1A', 'action': 'promote', 'next': 'JSS 2A'},
        {'id': 'S3', 'admission_number': 'P4/033', 'name': 'Dami Ogun', 'class': 'Primary 4', 'action': 'promote', 'next': 'Primary 5'},
        {'id': 'S4', 'admission_number': 'JSS2A/002', 'name': 'Efe Martins', 'class': 'JSS 2A', 'action': 'promote', 'next': 'JSS 3A'},
      ]});
  }
  if (p.endsWith('/fees') && p.startsWith('/v1/schools/terms/')) {
    Map<String, dynamic> c(String id, String n, int req, List<Map<String, dynamic>> items, [int pass = 0]) => {'class_id': id, 'class': n, 'required_kobo': req, 'pass_through_kobo': pass, 'items': items};
    final items = [
      {'name': 'Tuition', 'amount_kobo': 15000000, 'kind': 'required'},
      {'name': 'PTA levy', 'amount_kobo': 1000000, 'kind': 'required'},
      {'name': 'ICT levy', 'amount_kobo': 1500000, 'kind': 'required'},
      {'name': 'Bus, Ikeja route', 'amount_kobo': 3500000, 'kind': 'optional'},
      {'name': 'WAEC registration', 'amount_kobo': 1500000, 'kind': 'required', 'pass_through_for': 'WAEC'},
    ];
    return _j({'previous_term': {'name': 'Third term', 'session': '2025/2026'}, 'classes': [
      c('c0', 'JSS 1A', 19000000, items, 1500000), c('c1', 'JSS 1B', 17500000, items), c('c2', 'JSS 2A', 18000000, items), c('c3', 'Primary 4', 0, []),
    ]});
  }
  if (p == '/v1/schools/overview') {
    return _j({'overview': {
      'term': 'First term · 2026/2027', 'term_state': 'current', 'enrolled': 418, 'invoiced': 412, 'expected_kobo': 2594000000, 'expected_by_now_kobo': 2410000000, 'collected_kobo': 1842000000,
      'outstanding_kobo': 752000000, 'overdue_kobo': 318000000, 'collected_percent': 71, 'this_week_kobo': 231000000, 'pass_through_kobo': 0, 'pass_through': [],
      'matched': {'auto_kobo': 1, 'person_kobo': 1, 'auto_percent': 86},
      'how_paid': [{'how': 'account', 'payments': 300, 'kobo': 74}, {'how': 'transfer', 'payments': 30, 'kobo': 14}, {'how': 'cash', 'payments': 20, 'kobo': 9}, {'how': 'card', 'payments': 4, 'kobo': 3}],
      'top_debtors': [schoolDebtor('S2', 'Tolu Adeyemi', 'JSS 2A', 41, 18450000, 'over_30'),  schoolDebtor('S3', 'Kemi Bello', 'Primary 4', 23, 15200000, '14_30'),  schoolDebtor('S4', 'Chidi Okafor', 'SS 1B', 12, 9600000, '7_14')],
      'aging': {'not_due': {'students': 6}, '0_7': {'students': 12}, '7_14': {'students': 7}, '14_30': {'students': 4}, 'over_30': {'students': 3}},
      'debtors': 32, 'debtors_kobo': 752000000, 'clearance': {'students': 418, 'cleared': 341, 'partly_paid': 46, 'not_paid': 31},
      'last_close': {'id': 'x', 'day': '2026-11-16', 'ran_at': '2026-11-16T23:04:00Z', 'ran_by': 'platform', 'reconciles': true, 'problems': 0},
    }});
  }
  if (p == '/v1/schools/payments') {
    return _j({'postings': [
      {'id': 'P1', 'kind': 'cash_posting', 'status': 'pending', 'name': 'Amaka Obi', 'class': 'JSS 1A', 'invoice_number': 'INV-0412', 'amount_kobo': 5000000, 'details': {'how': 'cash'}, 'requested_email': 'bisi@greenfield.ng', 'requested_at': '2026-11-17T14:40:00Z', 'admission_number': 'JSS1A/014', 'student_id': 'S1'},
      {'id': 'P2', 'kind': 'discount', 'status': 'pending', 'name': 'Tolu Adeyemi', 'class': 'JSS 2A', 'amount_kobo': 3000000, 'details': {'kind': 'hardship', 'reason': 'hardship'}, 'requested_email': 'bisi@greenfield.ng', 'requested_at': '2026-11-16T10:00:00Z'},
      {'id': 'P3', 'kind': 'cash_posting', 'status': 'posted', 'name': 'Efe Martins', 'class': 'JSS 2A', 'invoice_number': 'R-00431', 'amount_kobo': 2000000, 'details': {'how': 'cash'}, 'requested_email': 'you@greenfield.ng', 'requested_at': '2026-11-16T10:00:00Z', 'decided_email': 'Ada'},
      {'id': 'P4', 'kind': 'cash_posting', 'status': 'declined', 'name': 'Dami Ogun', 'class': 'Primary 4', 'amount_kobo': 1500000, 'details': {'how': 'cash'}, 'requested_email': 'bisi@greenfield.ng', 'requested_at': '2026-11-13T10:00:00Z', 'decision_reason': 'no slip'},
    ]});
  }
  if (p == '/v1/schools/classes') return _j({'classes': schoolClasses});
  if (p == '/v1/schools/students') {
    return _j({'total': 418, 'students': [
      for (final (n, a, c, st) in [('Amaka Obi', 'JSS1A/014', 'JSS 1A', 'active'), ('Bolu Ade', 'JSS1A/021', 'JSS 1A', 'active'), ('Chidi Okafor', 'SS1B/007', 'SS 1B', 'active'), ('Dami Ogun', 'P4/033', 'Primary 4', 'withdrawn'), ('Efe Martins', 'JSS2A/002', 'JSS 2A', 'active'), ('Funke Ajayi', 'P4/011', 'Primary 4', 'active')])
        {'id': a, 'name': n, 'admission_number': a, 'class': c, 'status': st},
    ]});
  }
  if (p == '/v1/schools/students/S1') return _j({'student': schoolAmaka});
  if (p == '/v1/schools/students/S1/chasing') {
    return _j({'balance_kobo': 9600000, 'days_past_due': 12, 'bucket': '7_14', 'promises': [{'id': 'PR', 'promised_on': '2026-11-20', 'balance_kobo': 9600000, 'status': 'open'}],
      'reminders': [{'rung': 2, 'balance_kobo': 9600000, 'sent_on': '2026-11-14', 'channels': {'whatsapp': 1, 'sms': 1}}]});
  }
  if (p == '/v1/schools/students/S1/fees') {
    return _j({'term_id': 'T1', 'items': [{'id': 'bus', 'name': 'Bus, Ikeja route', 'amount_kobo': 3500000, 'kind': 'optional'}, {'id': 'x', 'name': 'Lessons', 'amount_kobo': 1, 'kind': 'optional'}], 'chosen': ['bus'],
      'lines': [{'description': 'Tuition', 'quantity': 1, 'unit_kobo': 15000000}, {'description': 'PTA levy', 'quantity': 1, 'unit_kobo': 1000000}, {'description': 'ICT levy', 'quantity': 1, 'unit_kobo': 1500000},
        {'description': 'Bus, Ikeja route', 'quantity': 1, 'unit_kobo': 3500000, 'kind': 'optional'}, {'description': 'Sibling discount 10%', 'quantity': 1, 'unit_kobo': -1500000}, {'description': 'WAEC registration', 'quantity': 1, 'unit_kobo': 1500000, 'pass_through_for': 'WAEC'}],
      'total_kobo': 21000000, 'invoice': {'invoice_id': 'I1', 'number': 'INV-0412', 'status': 'part_paid', 'term': 'First term', 'total_kobo': 21000000, 'paid_kobo': 11400000, 'due_kobo': 9600000}});
  }
  if (p == '/v1/schools/students/S1/accounts') return _j({'accounts': [{'status': 'issued', 'account_number': '9012345678', 'bank_name': 'Wema Bank', 'account_name': 'Greenfield Sch/Amaka Obi'}]});
  if (p == '/v1/schools/students/S1/clearance') return _j({'clearance': {'status': 'partly_paid', 'reason': 'balance', 'balance_kobo': 9600000}, 'overrides': []});
  if (p == '/v1/schools/students/S1/discounts') return _j({'discounts': [{'id': 'D', 'kind': 'sibling', 'percent': 10, 'reason': 'Brother in SS 1B'}]});
  if (p == '/v1/schools/plans' || p == '/v1/schools/claims') return _j({'plans': [], 'claims': []});
  if (p == '/v1/schools/receipts') {
    return _j({'receipts': [
      {'id': 'R3', 'number': 'R-00433', 'amount_kobo': 5000000, 'name': 'Amaka Obi', 'class': 'JSS 1A', 'invoice_number': 'INV-0412', 'how': 'cash', 'paid_at': '2026-11-17T14:52:00Z', 'balance_after_kobo': 4600000},
      {'id': 'R2', 'number': 'R-00432', 'amount_kobo': 21000000, 'name': 'Bolu Ade', 'class': 'JSS 1A', 'invoice_number': 'INV-0418', 'how': 'account', 'paid_at': '2026-11-17T10:03:00Z', 'balance_after_kobo': 0},
      {'id': 'R1', 'number': 'R-00431', 'amount_kobo': 2000000, 'name': 'Efe Martins', 'class': 'JSS 2A', 'invoice_number': 'INV-0399', 'how': 'cash', 'paid_at': '2026-11-16T10:00:00Z', 'balance_after_kobo': 7000000},
    ]});
  }
  if (p == '/v1/schools/debtors') {
    return _j({'students': 32, 'balance_kobo': 752000000,
      'totals': {'not_due': {'students': 6}, '0_7': {'students': 12}, '7_14': {'students': 7}, '14_30': {'students': 4}, 'over_30': {'students': 3}},
      'debtors': [
         schoolDebtor('S2', 'Tolu Adeyemi', 'JSS 2A', 41, 18450000, 'over_30', extra: {'last_reminder': {'rung': 3, 'at': '2026-11-09T10:00:00Z'}, 'plan': {'state': 'arrears'}}),
         schoolDebtor('S3', 'Kemi Bello', 'Primary 4', 23, 15200000, '14_30', extra: {'last_reminder': {'rung': 2, 'at': '2026-11-12T10:00:00Z'}}),
         schoolDebtor('S4', 'Chidi Okafor', 'SS 1B', 12, 9600000, '7_14', extra: {'promise': {'promised_on': '2026-11-20'}}),
      ]});
  }
  if (p == '/v1/schools/clearance') {
    return _j({'term': 'First term · 2026/2027', 'totals': {'students': 418, 'cleared': 341, 'partly_paid': 46, 'not_paid': 31}, 'students': [
      {'student_id': 'S2', 'name': 'Tolu Adeyemi', 'class': 'JSS 2A', 'status': 'not_paid', 'reason': 'balance', 'balance_kobo': 18450000, 'student_status': 'active'},
      {'student_id': 'S1', 'name': 'Amaka Obi', 'class': 'JSS 1A', 'status': 'partly_paid', 'reason': 'balance', 'balance_kobo': 9600000, 'student_status': 'active'},
      {'student_id': 'S5', 'name': 'Funke Ajayi', 'class': 'Primary 4', 'status': 'cleared', 'reason': 'plan', 'balance_kobo': 4000000, 'student_status': 'active'},
      {'student_id': 'S6', 'name': 'Dami Ogun', 'class': 'Primary 4', 'status': 'cleared', 'reason': 'override', 'balance_kobo': 4000000, 'student_status': 'active', 'override': {'id': 'O', 'reason': 'Scholarship letter coming', 'ends_on': '2026-12-04', 'in_force': true}},
    ]});
  }
  if (p == '/v1/gate/search') return _j({'students': [{'student_id': 'S1', 'name': 'Amaka Obi', 'class': 'JSS 1A'}, {'student_id': 'S7', 'name': 'Amara Eze', 'class': 'Primary 2'}, {'student_id': 'S8', 'name': 'Kamal Musa', 'class': 'SS 2A'}]});
  if (p == '/v1/gate/students/S8') return _j({'student_id': 'S8', 'name': 'Kamal Musa', 'class': 'SS 2A', 'status': 'not_paid'});
  if (p == '/v1/gate/students/S1') return _j({'student_id': 'S1', 'name': 'Amaka Obi', 'class': 'JSS 1A', 'status': 'cleared'});
  if (p == '/v1/schools/sessions') {
    return _j({'sessions': [{'id': 'SE', 'label': '2026/2027', 'terms': [
      {'id': 'T1', 'name': 'First term', 'starts_on': '2026-09-07', 'ends_on': '2026-12-18', 'fees_due_on': '2026-09-21', 'state': 'current', 'enrolled': 418},
      {'id': 'T2', 'name': 'Second term', 'starts_on': '2027-01-11', 'ends_on': '2027-04-09', 'fees_due_on': '2027-01-25', 'state': 'planned', 'enrolled': 0},
    ]}]});
  }
  if (p == '/v1/settings/schools') {
    return _j({'enabled': true, 'cash_threshold_naira': 20000, 'discount_threshold_naira': 10000, 'family_link_days': 7, 'plan_grace_days': 3,
      'issuing': {'connection': 'iswallet (main)', 'siblings_share_credit': false}, 'reminder_wording': {'2': 'Yours'}, 'reminder_defaults': {}});
  }
  if (p.startsWith('/v1/schools/closes/')) {
    return _j({'close': {'day': '2026-11-16', 'ran_at': '2026-11-16T23:04:00Z', 'ran_by': 'platform', 'reconciles': false, 'problems': 2,
      'money_in': {'count': 38, 'kobo': 61200000}, 'into_accounts': {'count': 31, 'kobo': 54400000}, 'at_the_desk': {'count': 7, 'kobo': 6800000}, 'recorded_late': {'count': 1, 'kobo': 1200000},
      'matched_auto': {'count': 29, 'kobo': 50100000}, 'matched_person': {'count': 6, 'kobo': 8500000}, 'unmatched': {'count': 3, 'kobo': 2600000}, 'lookback_found': {'count': 1, 'kobo': 800000},
      'unmatched_list': [{'kind': 'unmatched', 'message': 'transfer from ADEBAYO O.: no student’s account; ref 118204', 'path': '/invoices/match', 'amount_kobo': 1200000}],
      'pending': [{'kind': 'pending_posting', 'message': 'cash for JSS1A/014 asked by Bisi at 15:40', 'path': '/approvals', 'amount_kobo': 2000000}],
      'claims': [], 'problem_list': [{'kind': 'receipt_not_voided', 'message': 'a receipt with no payment: R-00412 · JSS 3B/020', 'path': '/schools/receipts', 'amount_kobo': 1500000}]}});
  }
  return null;
}


/// The briefing as drawn: a school's morning, yesterday closed.
const schoolBriefing = {
  'ready': true, 'day': '2026-11-17', 'key': 'briefing:2026-11-17', 'put_off': false, 'part': 'morning', 'now': '07:05',
  'school': {
    'term': 'First term 2026/2027', 'expected_kobo': 2594000000, 'collected_kobo': 1842000000, 'collected_percent': 71, 'this_week_kobo': 231000000, 'overdue_kobo': 318000000,
    'debtors': 32, 'debtors_kobo': 752000000, 'not_cleared': 77, 'cleared': 341, 'students': 418, 'waiting': 2,
    'yesterday': {'day': '2026-11-16', 'collected_kobo': 61200000, 'payments': 38, 'unmatched': 3, 'reconciles': true},
  },
};

/// Bisi's cash for Amaka, waiting for a second person, as a chat card.
const schoolApprovalAlert = {
  'key': 'school_approval:P1', 'kind': 'school_approval', 'at': '2026-11-17T06:02:00Z', 'title': 'Cash at the desk', 'line': 'JSS1A/014 · JSS 1A · INV-0412 · asked by bisi@greenfield.ng',
  'amount_kobo': 5000000, 'id': 'P1', 'who': 'Bisi',
  'school': {'id': 'P1', 'kind': 'cash_posting', 'status': 'pending', 'admission_number': 'JSS1A/014', 'name': 'A•••', 'class': 'JSS 1A', 'invoice_number': 'INV-0412',
    'amount_kobo': 5000000, 'details': {'how': 'cash'}, 'requested_by': 'user:x', 'requested_email': 'bisi@greenfield.ng', 'requested_at': '2026-11-17T06:02:00Z'},
};
