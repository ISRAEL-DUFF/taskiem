// The story's world for the Bright Hill Academy films of episodes 7 and 8
// (video/series/schools): the app's own school stand-in (school_fixture.dart)
// with the people renamed to the story's, and a few answers that CHANGE when
// the film sends something (a reminder, a promise, a plan approved, an
// override), so the screen shows what the real server would show next.
// Shared by film_school_debtors_test.dart and film_school_gate_test.dart;
// it is not a test.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/auth.dart' show Auth;
import 'package:taskiem/screens/home_tabs.dart';

import 'school_fixture.dart';
import 'support.dart';

http.Response bhJson(Object b) => http.Response.bytes(utf8.encode(jsonEncode(b)), 200, headers: {'content-type': 'application/json; charset=utf-8'});

String _day(DateTime d) => '${d.year}-${'${d.month}'.padLeft(2, '0')}-${'${d.day}'.padLeft(2, '0')}';

/// Mr Ibe, the bursar; or Mallam Audu, who has the gate role only.
void bhMe(Auth a, Fixture f, {bool gate = false}) {
  final me = Map<String, dynamic>.from(f.json('/v1/me'));
  me['tenant_name'] = 'Bright Hill Academy';
  me['user'] = {...(me['user'] as Map<String, dynamic>), 'name': gate ? 'Mallam Audu' : 'Chukwudi Ibe', 'email': gate ? 'audu@brighthill.example' : 'ibe@brighthill.example'};
  if (gate) {
    me['permissions'] = ['school.gate'];
    me['gate_only'] = true;
  }
  a.me = Me.fromJson(me);
}

/// The home's bottom bar as the app draws it for a school (Chats, Today,
/// Money, School); only School is real here, the others are blank. The film
/// opens on School before the first picture.
Widget bhHome(Widget school) => HomeTabs(
  tabs: [
    (label: 'Chats', icon: 'b-chat', build: (_) => const SizedBox.shrink()),
    (label: 'Today', icon: 'b-home', build: (_) => const SizedBox.shrink()),
    (label: 'Money', icon: 'b-money', build: (_) => const SizedBox.shrink()),
    (label: 'School', icon: 'b-school', build: (_) => school),
  ],
);

class BhPerson {
  const BhPerson(this.id, this.adm, this.name, this.cid, this.klass, this.guardian, this.rel, this.total, this.paid, this.days, this.tuition);
  final String id, adm, name, cid, klass, guardian, rel;
  final int total, paid, days, tuition;
}

const _people = {
  'S1': BhPerson('S1', 'JSS1A/014', 'Amaka Obi', 'c0', 'JSS 1A', 'Ngozi Obi', 'Mother', 10200000, 5000000, 12, 8500000),
  'S10': BhPerson('S10', 'JSS1A/009', 'Tobi Lawal', 'c0', 'JSS 1A', 'Bimpe Lawal', 'Mother', 10200000, 0, 12, 8500000),
  'S4': BhPerson('S4', 'SS1B/007', 'Chidi Okafor', 'c4', 'SS 1B', 'Ada Okafor', 'Mother', 9600000, 0, 9, 7900000),
};

class BhWorld {
  BhWorld() : now = DateTime.now();

  final DateTime now;
  final reminded = <String>{};
  final promises = <String, String>{}; // student id -> day promised
  String planState = 'requested'; // requested | approved
  List<Map<String, dynamic>> planInstalments = const [];
  bool amakaOverride = false;
  String overrideReason = '';

  DateTime _in(int days) => DateTime(now.year, now.month, now.day).add(Duration(days: days));

  /// What Mrs Lawal asked on the family page: three parts of ₦34,000.
  List<Map<String, dynamic>> get asked => [
    {'amount_kobo': 3400000, 'due_on': _day(_in(3))},
    {'amount_kobo': 3400000, 'due_on': _day(_in(17))},
    {'amount_kobo': 3400000, 'due_on': _day(_in(45))},
  ];

  Map<String, dynamic> get _plan => {
    'id': 'PL',
    'status': planState,
    'student_id': 'S10',
    'name': 'Tobi Lawal',
    'class': 'JSS 1A',
    'message': 'Please may we pay the term in three parts? Bimpe Lawal',
    'created_at': DateTime.now().toUtc().subtract(const Duration(hours: 2)).toIso8601String(),
    'instalments': planState == 'approved' ? planInstalments : asked,
  };

  List<Map<String, dynamic>> debtors() {
    Map<String, dynamic> extraFor(String id) => {
      if (reminded.contains(id)) 'last_reminder': {'rung': 2, 'at': DateTime.now().toUtc().toIso8601String()},
      if (promises.containsKey(id)) 'promise': {'promised_on': promises[id]},
      if (id == 'S10' && planState == 'approved') 'plan': {'state': 'ok'},
    };
    final tolu = schoolDebtor('S2', 'Tolu Adeyemi', 'JSS 2A', 41, 18450000, 'over_30', extra: {'last_reminder': {'rung': 3, 'at': _day(_in(-15))}, 'plan': {'state': 'arrears'}});
    final kemi = schoolDebtor('S3', 'Kemi Bello', 'Primary 4', 23, 15200000, '14_30', extra: {'last_reminder': {'rung': 2, 'at': _day(_in(-9))}});
    return [
      tolu,
      kemi,
      schoolDebtor('S10', 'Tobi Lawal', 'JSS 1A', 12, 10200000, '7_14', extra: extraFor('S10')),
      schoolDebtor('S4', 'Chidi Okafor', 'SS 1B', 9, 9600000, '7_14', extra: extraFor('S4')),
    ];
  }

  Map<String, dynamic> studentJson(BhPerson p) => {
    'student': {
      'id': p.id,
      'admission_number': p.adm,
      'name': p.name,
      'class_id': p.cid,
      'class': p.klass,
      'status': 'active',
      'family_id': 'F-${p.id}',
      'family': {
        'id': 'F-${p.id}',
        'guardians': [
          {'id': 'G-${p.id}', 'name': p.guardian, 'phone': '0803 ••• 4412', 'relationship': p.rel},
        ],
        'students': [
          {'id': p.id, 'admission_number': p.adm, 'name': p.name, 'class': p.klass, 'status': 'active'},
        ],
      },
      'enrollments': [
        {'term': 'First term', 'session': '2026/2027', 'term_state': 'current', 'class': p.klass},
      ],
    },
  };

  Map<String, dynamic> chasing(BhPerson p) {
    final bal = p.total - p.paid;
    final plan = p.id == 'S10' && planState == 'approved'
        ? {'state': 'ok', 'instalments': planInstalments, 'next_due_on': planInstalments.first['due_on'], 'next_amount_kobo': planInstalments.first['amount_kobo']}
        : null;
    return {
      'balance_kobo': bal,
      'days_past_due': p.days,
      'bucket': p.days > 7 ? '7_14' : '0_7',
      'promises': [
        if (promises.containsKey(p.id)) {'id': 'PR-${p.id}', 'promised_on': promises[p.id], 'balance_kobo': bal, 'status': 'open'},
      ],
      'reminders': [
        if (reminded.contains(p.id)) {'rung': 2, 'balance_kobo': bal, 'sent_on': _day(now), 'channels': {'whatsapp': 1}},
      ],
      'plan': ?plan,
    };
  }

  Map<String, dynamic> fees(BhPerson p) {
    final bal = p.total - p.paid;
    return {
      'term_id': 'T1',
      'items': [
        {'id': 'bus', 'name': 'Transport', 'amount_kobo': 2000000, 'kind': 'optional'},
      ],
      'chosen': <String>[],
      'lines': [
        {'description': 'Tuition', 'quantity': 1, 'unit_kobo': p.tuition},
        {'description': 'PTA levy', 'quantity': 1, 'unit_kobo': 500000},
        {'description': 'Books', 'quantity': 1, 'unit_kobo': 1200000},
      ],
      'total_kobo': p.total,
      'invoice': {'invoice_id': 'I-${p.id}', 'number': 'INV-0${400 + p.id.length + p.adm.length}', 'status': p.paid > 0 ? 'part_paid' : 'sent', 'term': 'First term', 'total_kobo': p.total, 'paid_kobo': p.paid, 'due_kobo': bal},
    };
  }

  /// Clearance as the server works it out: a plan in good standing or an
  /// override clears; a part-payment or nothing does not.
  Map<String, dynamic> clearanceOf(BhPerson p) {
    if (p.id == 'S10' && planState == 'approved') return {'status': 'cleared', 'reason': 'plan'};
    if (p.id == 'S1' && amakaOverride) return {'status': 'cleared', 'reason': 'override'};
    return {'status': p.paid > 0 ? 'partly_paid' : 'not_paid', 'reason': 'balance', 'balance_kobo': p.total - p.paid};
  }

  Map<String, dynamic> get _amakaOverride => {'id': 'OV', 'reason': overrideReason, 'ends_on': '2026-12-18', 'in_force': true};

  Map<String, dynamic> clearance({String classId = ''}) {
    final rows = <Map<String, dynamic>>[
      {'student_id': 'S2', 'name': 'Tolu Adeyemi', 'class': 'JSS 2A', 'cid': 'c2', 'status': 'not_paid', 'reason': 'balance', 'balance_kobo': 18450000, 'student_status': 'active'},
      {'student_id': 'S1', 'name': 'Amaka Obi', 'class': 'JSS 1A', 'cid': 'c0', 'status': amakaOverride ? 'cleared' : 'partly_paid', 'reason': amakaOverride ? 'override' : 'balance', 'balance_kobo': 5200000, 'student_status': 'active', if (amakaOverride) 'override': _amakaOverride},
      {'student_id': 'S11', 'name': 'Bolu Ade', 'class': 'JSS 1A', 'cid': 'c0', 'status': 'not_paid', 'reason': 'balance', 'balance_kobo': 10200000, 'student_status': 'active'},
      {'student_id': 'S3', 'name': 'Kemi Bello', 'class': 'Primary 4', 'cid': 'c3', 'status': 'not_paid', 'reason': 'balance', 'balance_kobo': 15200000, 'student_status': 'active'},
      {'student_id': 'S10', 'name': 'Tobi Lawal', 'class': 'JSS 1A', 'cid': 'c0', 'status': planState == 'approved' ? 'cleared' : 'not_paid', 'reason': planState == 'approved' ? 'plan' : 'balance', 'balance_kobo': 10200000, 'student_status': 'active'},
      {'student_id': 'S6', 'name': 'Dami Ogun', 'class': 'Primary 4', 'cid': 'c3', 'status': 'cleared', 'reason': 'paid', 'balance_kobo': 0, 'student_status': 'active'},
    ];
    final out = [
      for (final r in rows)
        if (classId.isEmpty || r['cid'] == classId) {...r}..remove('cid'),
    ];
    return {
      'term': 'First term · 2026/2027',
      'totals': {'students': 418, 'cleared': amakaOverride ? 342 : 341, 'partly_paid': amakaOverride ? 45 : 46, 'not_paid': 31},
      'students': out,
    };
  }

  /// The gate's children, by name; each with whether they may come in.
  List<Map<String, dynamic>> get gateKids => [
    {'student_id': 'S1', 'name': 'Amaka Obi', 'class': 'JSS 1A', 'status': amakaOverride ? 'cleared' : 'partly_paid'},
    {'student_id': 'S7', 'name': 'Amara Eze', 'class': 'Primary 2', 'status': 'cleared'},
    {'student_id': 'S3', 'name': 'Kemi Bello', 'class': 'Primary 4', 'status': 'not_paid'},
    {'student_id': 'S10', 'name': 'Tobi Lawal', 'class': 'JSS 1A', 'status': planState == 'approved' ? 'cleared' : 'not_paid'},
  ];

  Future<http.Response?> server(http.Request req) async {
    final p = req.url.path;
    if (req.method == 'POST') {
      final body = req.body.isEmpty ? <String, dynamic>{} : jsonDecode(req.body) as Map<String, dynamic>;
      if (p == '/v1/schools/reminders') {
        final ids = (body['students'] as List).cast<String>();
        reminded.addAll(ids);
        return bhJson({'sent': ids.length, 'skipped': 0});
      }
      final promise = RegExp(r'^/v1/schools/students/(\w+)/promise$').firstMatch(p);
      if (promise != null) {
        promises[promise.group(1)!] = body['date'] as String;
        return bhJson({});
      }
      if (p == '/v1/schools/plans/PL') {
        planState = 'approved';
        planInstalments = ((body['instalments'] as List?) ?? asked).cast<Map<String, dynamic>>();
        return bhJson({});
      }
      if (RegExp(r'^/v1/schools/students/S1/clearance-override$').hasMatch(p)) {
        amakaOverride = true;
        overrideReason = body['reason'] as String;
        return bhJson({});
      }
      return null;
    }
    if (req.method != 'GET') return null;
    if (p == '/v1/schools/overview') {
      final base = jsonDecode((await schoolServer(req))!.body) as Map<String, dynamic>;
      final o = {
        ...(base['overview'] as Map<String, dynamic>),
        'expected_kobo': 4280000000,
        'expected_by_now_kobo': 3900000000,
        'collected_kobo': 2996000000,
        'outstanding_kobo': 1284000000,
        'overdue_kobo': 640000000,
        'collected_percent': 70,
        'debtors': 118,
        'debtors_kobo': 1284000000,
        'aging': {'not_due': {'students': 14}, '0_7': {'students': 38}, '7_14': {'students': 31}, '14_30': {'students': 24}, 'over_30': {'students': 11}},
        'top_debtors': [
          schoolDebtor('S2', 'Tolu Adeyemi', 'JSS 2A', 41, 18450000, 'over_30'),
          schoolDebtor('S3', 'Kemi Bello', 'Primary 4', 23, 15200000, '14_30'),
          schoolDebtor('S10', 'Tobi Lawal', 'JSS 1A', 12, 10200000, '7_14'),
        ],
      };
      return bhJson({'overview': o});
    }
    if (p == '/v1/schools/debtors') {
      final q = req.url.queryParameters;
      final all = debtors();
      final shown = [
        for (final d in all)
          if ((q['bucket'] ?? '').isEmpty || d['bucket'] == q['bucket']) d,
      ];
      return bhJson({
        'students': (q['bucket'] ?? '').isEmpty ? 118 : shown.length,
        'balance_kobo': (q['bucket'] ?? '').isEmpty ? 1284000000 : shown.fold<int>(0, (n, d) => n + (d['balance_kobo'] as int)),
        'totals': {'not_due': {'students': 14}, '0_7': {'students': 38}, '7_14': {'students': 31}, '14_30': {'students': 24}, 'over_30': {'students': 11}},
        'debtors': shown,
      });
    }
    if (p == '/v1/schools/clearance') return bhJson(clearance(classId: req.url.queryParameters['class'] ?? ''));
    if (p == '/v1/schools/plans') return bhJson({'plans': req.url.queryParameters['student'] == 'S10' ? [_plan] : <Object>[]});
    if (p == '/v1/schools/claims') return bhJson({'claims': <Object>[]});
    final st = RegExp(r'^/v1/schools/students/(\w+)(/\w+)?$').firstMatch(p);
    final person = st == null ? null : _people[st.group(1)];
    if (st != null && person != null) {
      switch (st.group(2)) {
        case null:
          return bhJson(studentJson(person));
        case '/chasing':
          return bhJson(chasing(person));
        case '/fees':
          return bhJson(fees(person));
        case '/accounts':
          return bhJson({
            'accounts': [
              {'status': 'issued', 'account_number': person.id == 'S1' ? '9012345678' : (person.id == 'S10' ? '9012345702' : '9012345719'), 'bank_name': 'Wema Bank', 'account_name': 'Bright Hill/${person.name}'},
            ],
          });
        case '/clearance':
          return bhJson({
            'clearance': clearanceOf(person),
            'overrides': [if (person.id == 'S1' && amakaOverride) _amakaOverride],
          });
        case '/discounts':
          return bhJson({'discounts': <Object>[]});
      }
    }
    if (p == '/v1/gate/search') {
      final q = (req.url.queryParameters['q'] ?? '').toLowerCase();
      return bhJson({
        'students': [
          for (final k in gateKids)
            if ((k['name'] as String).toLowerCase().contains(q)) {'student_id': k['student_id'], 'name': k['name'], 'class': k['class']},
        ],
      });
    }
    final gate = RegExp(r'^/v1/gate/students/(\w+)$').firstMatch(p);
    if (gate != null) {
      final k = gateKids.firstWhere((k) => k['student_id'] == gate.group(1));
      return bhJson({'student_id': k['student_id'], 'name': k['name'], 'class': k['class'], 'status': k['status'] == 'cleared' ? 'cleared' : 'not_paid'});
    }
    return schoolServer(req);
  }
}

/// Lets what the stand-in server answers come in, without a picture.
Future<void> bhOpen(WidgetTester t, Widget app) async {
  await t.pumpWidget(app);
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pump(const Duration(seconds: 1));
}
