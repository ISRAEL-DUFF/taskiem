// The world of the Schools films, episodes 9 and 10 (Bright Hill Academy,
// Ikorodu): a stand-in server for the school's half of the API, built on the
// app's own school_fixture.dart (schoolServer) with the story's numbers laid
// over it; the signed-in person is Mr Chukwudi Ibe, the bursar, or a clerk
// who may not see names. Used by film_school_overview_test.dart and
// film_school_safe_next_term_test.dart.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/money_page.dart' show MoneyPage;
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/chat/today_tab.dart' show TodayTab;
import 'package:taskiem/kit/mobile.dart' show TkRoute;
import 'package:taskiem/school/school_tab.dart';
import 'package:taskiem/screens/home_tabs.dart';

import 'school_fixture.dart';
import 'support.dart';

http.Response bhJson(Object b, [int s = 200]) => http.Response(jsonEncode(b), s, headers: {'content-type': 'application/json'});

/// Tuesday 17 November 2026, 07:05 in Lagos (06:05 UTC).
final bhMorning = DateTime.utc(2026, 11, 17, 6, 5);

/// What a request reads: the school's answers with the story's numbers laid
/// over them. Names are masked unless the request asks to `reveal` (the app
/// asks only when the person may: pii.reveal), as the real server does.
class BhWorld {
  BhWorld({this.promoted = false, this.started = false, this.copied = false});

  /// Episode 10 moves on as the person acts.
  bool promoted;
  bool started;
  bool copied;
  final sent = <String>[];
  final asked = <String>[];

  /// A masked name: first and last letters of each word kept, the rest dots.
  static String mask(String n) => n.split(' ').map((w) => w.length < 3 ? w : '${w[0]}•••${w[w.length - 1]}').join(' ');

  static const people = [
    ('Tobi Lawal', 'JSS1A/031', 'JSS 1A', 'active'),
    ('Amaka Obi', 'JSS1A/014', 'JSS 1A', 'active'),
    ('Dayo Danjuma', 'P4/012', 'Primary 4', 'active'),
    ('Bolu Ade', 'JSS1A/021', 'JSS 1A', 'active'),
    ('Chidi Okafor', 'SS1B/007', 'SS 1B', 'active'),
    ('Efe Martins', 'JSS2A/002', 'JSS 2A', 'active'),
  ];

  Future<http.Response?> answer(http.Request r) async {
    final p = r.url.path;
    final reveal = r.url.queryParameters['reveal'] == 'true';
    String nm(String n) => reveal ? n : mask(n);
    asked.add('${r.method} $p');
    if (r.method != 'GET') {
      sent.add('${r.method} $p');
      if (p.endsWith('/promotion')) {
        promoted = true;
        return bhJson(_promotion(reveal));
      }
      if (p == '/v1/schools/terms/T4/start') {
        started = true;
        return bhJson({'enrolled': 400});
      }
      if (p == '/v1/schools/terms/T4/fees/c0/copy') {
        copied = true;
        return bhJson({'ok': true});
      }
      return null;
    }
    switch (p) {
      case '/v1/links':
        return bhJson({'links': <Object>[], 'sent': <Object>[]});
      case '/v1/link-chats':
        return bhJson({'conversations': <Object>[]});
      case '/v1/taskiem-chat':
        return bhJson({'messages': <Object>[]});
      case '/v1/chat-briefing':
        return bhJson(bhBriefing);
      case '/v1/chat-alerts':
        return bhJson({'alerts': [schoolApprovalAlert]});
      case '/v1/schools/overview':
        return bhJson({'overview': _overview});
      case '/v1/schools/students':
        return bhJson({'total': 400, 'students': [for (final (n, a, c, s) in people) {'id': a == 'JSS1A/014' ? 'S1' : a, 'name': nm(n), 'admission_number': a, 'class': c, 'status': s}]});
      case '/v1/schools/students/S1':
        return bhJson({'student': {
          ...schoolAmaka,
          'name': nm('Amaka Obi'),
          'family': {'id': 'F1', 'guardians': [{'id': 'G1', 'name': nm('Ngozi Obi'), 'phone': reveal ? '0803 112 4412' : '0803 ••• 4412', 'relationship': 'Mother'}], 'students': [
            {'id': 'S1', 'admission_number': 'JSS1A/014', 'name': nm('Amaka Obi'), 'class': 'JSS 1A', 'status': 'active'},
            {'id': 'S9', 'admission_number': 'SS1B/019', 'name': nm('Ifeanyi Obi'), 'class': 'SS 1B', 'status': 'active'},
          ]},
          'enrollments': [{'term': 'Third term', 'session': '2026/2027', 'term_state': 'current', 'class': 'JSS 1A'}],
        }});
      case '/v1/schools/sessions':
        return bhJson(_sessions);
      case '/v1/schools/sessions/SE/promotion':
        return bhJson(_promotion(reveal));
      case '/v1/schools/terms/T4/fees':
        return bhJson(_nextTermFees);
    }
    return schoolServer(r);
  }

  Map<String, dynamic> _promotion(bool reveal) => {
    'session': '2026/2027', 'done': promoted, 'promoted_at': promoted ? '2027-07-24T10:00:00Z' : null,
    'classes': [
      {'class': 'Primary 4', 'next': 'Primary 5', 'students': 27},
      {'class': 'JSS 1A', 'next': 'JSS 2A', 'students': 32},
      {'class': 'JSS 3A', 'next': 'SS 1A', 'students': 28},
      {'class': 'SS 3A', 'next': '', 'students': 24},
    ],
    'students': [
      {'id': 'S2', 'admission_number': 'JSS1A/021', 'name': reveal ? 'Bolu Ade' : 'B•••u Ade', 'class': 'JSS 1A', 'action': 'promote', 'next': 'JSS 2A'},
      {'id': 'S3', 'admission_number': 'P4/033', 'name': reveal ? 'Dami Ogun' : 'D•••i O••', 'class': 'Primary 4', 'action': 'promote', 'next': 'Primary 5'},
    ],
  };

  Map<String, dynamic> get _sessions => {'sessions': [
    {'id': 'SE', 'label': '2026/2027', 'promoted_at': promoted ? '2027-07-24T10:00:00Z' : null, 'terms': [
      {'id': 'T1', 'name': 'First term', 'starts_on': '2026-09-07', 'ends_on': '2026-12-18', 'fees_due_on': '2026-09-21', 'state': 'past', 'enrolled': 400},
      {'id': 'T2', 'name': 'Second term', 'starts_on': '2027-01-11', 'ends_on': '2027-04-09', 'fees_due_on': '2027-01-25', 'state': 'past', 'enrolled': 400},
      {'id': 'T3', 'name': 'Third term', 'starts_on': '2027-04-26', 'ends_on': '2027-07-23', 'fees_due_on': '2027-05-10', 'state': started ? 'past' : 'current', 'enrolled': 400},
    ]},
    {'id': 'SE2', 'label': '2027/2028', 'terms': [
      {'id': 'T4', 'name': 'First term', 'starts_on': '2027-09-06', 'ends_on': '2027-12-17', 'fees_due_on': '2027-09-20', 'state': started ? 'current' : 'planned', 'enrolled': started ? 400 : 0},
    ]},
  ]};

  Map<String, dynamic> get _nextTermFees {
    Map<String, dynamic> c(String id, String n, List<Map<String, dynamic>> items) => {
      'class_id': id, 'class': n, 'required_kobo': items.where((i) => i['kind'] == 'required').fold<int>(0, (a, i) => a + (i['amount_kobo'] as int)), 'pass_through_kobo': 0, 'items': items,
    };
    final items = copied
        ? <Map<String, dynamic>>[
            {'name': 'Tuition', 'amount_kobo': 8500000, 'kind': 'required'},
            {'name': 'PTA levy', 'amount_kobo': 500000, 'kind': 'required'},
            {'name': 'Books', 'amount_kobo': 1200000, 'kind': 'required'},
            {'name': 'Transport', 'amount_kobo': 2000000, 'kind': 'optional'},
          ]
        : <Map<String, dynamic>>[];
    return {
      'previous_term': {'name': 'Third term', 'session': '2026/2027'},
      'classes': [c('c0', 'JSS 1A', items), c('c1', 'JSS 1B', const []), c('c2', 'JSS 2A', const []), c('c3', 'Primary 4', const [])],
    };
  }
}

/// The overview in the story's numbers: expected ₦42,800,000, collected
/// ₦29,960,000 (70%); the last close is yesterday's and does not reconcile.
final _overview = <String, dynamic>{
  'term': 'First term · 2026/2027', 'term_state': 'current', 'enrolled': 400, 'invoiced': 400, 'expected_kobo': 4280000000, 'expected_by_now_kobo': 4100000000, 'collected_kobo': 2996000000,
  'outstanding_kobo': 1284000000, 'overdue_kobo': 612000000, 'collected_percent': 70, 'this_week_kobo': 231000000, 'pass_through_kobo': 0, 'pass_through': [],
  'matched': {'auto_kobo': 1, 'person_kobo': 1, 'auto_percent': 86},
  'how_paid': [{'how': 'account', 'payments': 300, 'kobo': 74}, {'how': 'transfer', 'payments': 30, 'kobo': 14}, {'how': 'cash', 'payments': 20, 'kobo': 9}, {'how': 'card', 'payments': 4, 'kobo': 3}],
  'top_debtors': [
    schoolDebtor('S2', 'Tolu Adeyemi', 'JSS 2A', 41, 18450000, 'over_30'),
    schoolDebtor('S3', 'Kemi Bello', 'Primary 4', 23, 15200000, '14_30'),
    schoolDebtor('S4', 'Chidi Okafor', 'SS 1B', 12, 9600000, '7_14'),
  ],
  'aging': {'not_due': {'students': 21}, '0_7': {'students': 38}, '7_14': {'students': 27}, '14_30': {'students': 19}, 'over_30': {'students': 11}},
  'debtors': 116, 'debtors_kobo': 1284000000, 'clearance': {'students': 400, 'cleared': 291, 'partly_paid': 71, 'not_paid': 38},
  'last_close': {'id': 'x', 'day': '2026-11-16', 'ran_at': '2026-11-16T23:04:00Z', 'ran_by': 'platform', 'reconciles': false, 'problems': 2},
};

/// The morning briefing, in the story's numbers.
const bhBriefing = {
  'ready': true, 'day': '2026-11-17', 'key': 'briefing:2026-11-17', 'put_off': false, 'part': 'morning', 'now': '07:05',
  'school': {
    'term': 'First term 2026/2027', 'expected_kobo': 4280000000, 'collected_kobo': 2996000000, 'collected_percent': 70, 'this_week_kobo': 231000000, 'overdue_kobo': 612000000,
    'debtors': 116, 'debtors_kobo': 1284000000, 'not_cleared': 109, 'cleared': 291, 'students': 400, 'waiting': 1,
    'yesterday': {'day': '2026-11-16', 'collected_kobo': 61200000, 'payments': 38, 'unmatched': 3, 'reconciles': false},
  },
};

/// Signed in as the bursar, or (reveal: false) a clerk who may not see names.
ChatsData bhChats(BhWorld w, {bool reveal = true, DateTime? now}) {
  final f = Fixture('chats');
  final auth = f.auth(also: w.answer);
  final j = Map<String, dynamic>.from(f.json('/v1/me'));
  j['schools'] = true;
  j['areas'] = {...(j['areas'] as Map<String, dynamic>), 'schools': true};
  j['tenant_name'] = 'Bright Hill Academy';
  j['user'] = {...(j['user'] as Map<String, dynamic>), 'name': reveal ? 'Chukwudi Ibe' : 'Funmi Bello'};
  j['permissions'] = [for (final p in j['permissions'] as List) if (reveal || p != 'pii.reveal') p];
  auth.me = Me.fromJson(j);
  return ChatsData(auth, now: () => now ?? bhMorning)..load();
}

/// The app's own bottom bar for a school: Chats, Today, Money, School; the
/// chats open Taskiem's conversation as the app's own Chats tab does.
Widget bhHome(ChatsData chats) => HomeTabs(
  count: () => 0,
  tabs: [
    (
      label: 'Chats',
      icon: 'b-chat',
      build: (c) => ChatsScreen(
        data: chats,
        onLinks: () {},
        onSearchAll: () {},
        onSwitch: () {},
        onProfile: () {},
        onMenu: () {},
        onOpenTaskiem: () => Navigator.of(c).push(TkRoute<void>(pageBuilder: (_, _, _) => TaskiemScreen(data: TaskiemData(chats, follow: false), onBack: () => Navigator.of(c).pop()))),
        onOpenCustomer: (_) {},
        onOpen: (_) {},
        onOpenAt: (_, _) {},
      ),
    ),
    (label: 'Today', icon: 'b-home', build: (c) => TodayTab(auth: chats.auth, onSearch: () {})),
    (label: 'Money', icon: 'b-money', build: (_) => MoneyPage(auth: chats.auth, tab: true)),
    (label: 'School', icon: 'b-school', build: (_) => SchoolTab(auth: chats.auth)),
  ],
);

/// Lets what the stand-in server answers come in.
Future<void> bhLoad(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
