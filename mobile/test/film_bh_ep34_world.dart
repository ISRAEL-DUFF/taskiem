// The stand-in server for the Bright Hill Academy films of Episodes 3 and 4 (Students and families,
// Set the fees): school_fixture.dart's recorded answers (schoolServer) with the story's students,
// classes and fees in place of the fixture's, and answers that change as the film acts (the import
// adds students, an added student appears, saved fees are read back). Not a test; the
// film_school_*_test.dart files use it. The app is the real signed-in shell (the bottom bar with
// the School tab), as the app's own school_test.dart opens its pages, but through TaskiemApp(auth:).

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/money_page.dart' show pickStatement;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'school_fixture.dart';
import 'support.dart';

http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});

const _firsts = ['Aisha', 'Bolu', 'Chidi', 'Damilola', 'Efe', 'Folake', 'Gbenga', 'Hauwa', 'Ife', 'Jide', 'Kemi', 'Lanre', 'Maryam', 'Nkem', 'Ola', 'Peter', 'Ridwan', 'Sade', 'Tunde', 'Uche', 'Wale', 'Yemi', 'Zainab'];
const _lasts = ['Adeyemi', 'Bello', 'Okafor', 'Ogun', 'Martins', 'Ajayi', 'Balogun', 'Eze', 'Musa', 'Nwosu', 'Ade', 'Ibrahim', 'Okoro', 'Salami'];
const _classNames = ['JSS 1A', 'JSS 1B', 'JSS 2A', 'Primary 4', 'SS 1B'];

/// Bright Hill Academy's school, changing as the film acts. [fresh] is a school with no students yet (Episode 3).
class BrightHill {
  BrightHill({this.fresh = true}) {
    if (!fresh) {
      imported = true;
      added = true;
    }
  }

  final bool fresh;
  final List<String> sent = [];
  bool imported = false;
  bool added = false;
  bool saved = false;
  bool copied = false;

  /// The 399 the import brings (the list shows the first 60: a stand-in's shortcut).
  List<Map<String, dynamic>> get imports => [
    {'id': 'P4/011', 'name': 'Dayo Danjuma', 'admission_number': 'P4/011', 'class': 'Primary 4', 'status': 'active'},
    for (var i = 0; i < 59; i++)
      {
        'id': 'BH/${100 + i}',
        'name': '${_firsts[(i * 7) % _firsts.length]} ${_lasts[(i * 5) % _lasts.length]}',
        'admission_number': '${['JSS1A', 'JSS1B', 'JSS2A', 'P4', 'SS1B'][i % 5]}/${(i * 3 + 20).toString().padLeft(3, '0')}',
        'class': _classNames[i % 5],
        'status': 'active',
      },
  ];

  static const tobi = {'id': 'S400', 'name': 'Tobi Lawal', 'admission_number': 'JSS1A/041', 'class': 'JSS 1A', 'status': 'active'};

  int get total => (imported ? 399 : 0) + (added ? 1 : 0);

  Map<String, dynamic> _classRow(int i) => {'id': 'c$i', 'name': _classNames[i], 'label': _classNames[i], 'order': i, 'students': imported ? [80, 80, 78, 82, 79][i] : 0};

  // JSS 1A's fees, as the story has them; "Third term" is what Copy from brings in.
  static const _lastTerm = [
    {'name': 'Tuition', 'amount_kobo': 8000000, 'kind': 'required'},
    {'name': 'PTA levy', 'amount_kobo': 500000, 'kind': 'required'},
    {'name': 'Books', 'amount_kobo': 1200000, 'kind': 'required'},
    {'name': 'Transport', 'amount_kobo': 2000000, 'kind': 'optional'},
  ];
  List<Map<String, dynamic>> jss1a = [];

  Map<String, dynamic> _fees() {
    Map<String, dynamic> c(int i, List<Map<String, dynamic>> items) =>
        {'class_id': 'c$i', 'class': _classNames[i], 'required_kobo': items.where((x) => x['kind'] == 'required').fold<int>(0, (n, x) => n + (x['amount_kobo'] as int)), 'pass_through_kobo': 0, 'items': items};
    return {
      'previous_term': {'name': 'Third term', 'session': '2025/2026'},
      'classes': [c(0, jss1a), for (var i = 1; i < 5; i++) c(i, const [])],
    };
  }

  Map<String, dynamic> _overview() {
    final o = Map<String, dynamic>.from((jsonDecode(jsonEncode(_over)) as Map).cast<String, dynamic>());
    o['enrolled'] = total;
    o['invoiced'] = 0;
    o['expected_kobo'] = 0;
    o['expected_by_now_kobo'] = 0;
    o['collected_kobo'] = 0;
    o['collected_percent'] = 0;
    o['outstanding_kobo'] = 0;
    o['overdue_kobo'] = 0;
    o['this_week_kobo'] = 0;
    o['top_debtors'] = [];
    o['debtors'] = 0;
    o['debtors_kobo'] = 0;
    o['aging'] = {'not_due': {'students': 0}, '0_7': {'students': 0}, '7_14': {'students': 0}, '14_30': {'students': 0}, 'over_30': {'students': 0}};
    o['how_paid'] = [];
    o['clearance'] = {'students': total, 'cleared': 0, 'partly_paid': 0, 'not_paid': total};
    o['last_close'] = null;
    return o;
  }

  Map<String, dynamic> _over = {};

  Future<http.Response?> answer(http.Request req) async {
    final p = req.url.path;
    final m = req.method;
    if (m != 'GET') sent.add('$m $p');
    if (m == 'POST' && p == '/v1/schools/students/import') {
      final body = jsonDecode(req.body) as Map<String, dynamic>;
      final confirmed = body['confirm'] != null;
      if (confirmed) imported = true;
      return ok({
        'hash': 'h',
        'added': 399,
        'updated': 0,
        'saved': confirmed,
        'students': List.generate(399, (i) => {'row': i + 2}),
        'families': [
          {'index': 0, 'existing': false, 'guardians': [{'name': 'Mr Segun Danjuma', 'phone': '08037781204'}], 'students': ['Dayo Danjuma']},
          {'index': 1, 'existing': false, 'guardians': [{'name': 'Mrs Ngozi Okafor', 'phone': '08052214498'}], 'students': ['Chidi Okafor', 'Kemi Okafor']},
          {'index': 2, 'existing': false, 'guardians': [{'name': 'Alhaji Musa Bello', 'phone': '08029914476'}], 'students': ['Hauwa Bello']},
        ],
      });
    }
    if (m == 'POST' && p == '/v1/schools/students') {
      added = true;
      return ok({'id': tobi['id'], 'student': tobi});
    }
    if (m == 'PUT' && p == '/v1/schools/terms/T1/fees/c0') {
      jss1a = ((jsonDecode(req.body) as Map)['items'] as List).cast<Map<String, dynamic>>();
      saved = true;
      return ok({});
    }
    if (m == 'POST' && p == '/v1/schools/terms/T1/fees/c0/copy') {
      jss1a = [for (final x in _lastTerm) {...x}];
      copied = true;
      return ok({});
    }
    if (m != 'GET') return null;
    if (p == '/v1/schools/classes') return ok({'classes': [for (var i = 0; i < 5; i++) _classRow(i)]});
    if (p == '/v1/schools/students') {
      final q = req.url.queryParameters;
      var rows = [if (added) tobi, ...(imported ? imports : [])];
      final cls = q['class'] ?? '';
      if (cls.isNotEmpty) rows = rows.where((r) => r['class'] == _classNames[int.parse(cls.substring(1))]).toList();
      return ok({'total': cls.isEmpty ? total : rows.length, 'students': rows});
    }
    if (p == '/v1/schools/students/${tobi['id']}') {
      return ok({'student': {
        ...tobi,
        'class_id': 'c0',
        'family_id': 'F40',
        'family': {'id': 'F40', 'guardians': [{'id': 'G40', 'name': 'Mrs Bimpe Lawal', 'phone': '0803 ••• 3318', 'relationship': 'Mother'}], 'students': [tobi]},
        'enrollments': [{'term': 'First term', 'session': '2026/2027', 'term_state': 'current', 'class': 'JSS 1A'}],
      }});
    }
    if (p == '/v1/schools/overview') return ok({'overview': _overview()});
    if (p.endsWith('/fees') && p.startsWith('/v1/schools/terms/')) return ok(_fees());
    return null;
  }

  /// Both: this world first, then the app's own school stand-in (school_fixture.dart).
  Future<http.Response?> serve(http.Request req) async => await answer(req) ?? await schoolServer(req);

  /// The real signed-in app (the bottom bar: Chats, Today, Money, School), as Mr Ibe, the bursar of Bright Hill Academy.
  Future<ChatsData> open(WidgetTester t, {Future<http.Response?> Function(http.Request r)? also}) async {
    final f = Fixture('chats');
    final over = await schoolServer(http.Request('GET', Uri.parse('http://x/v1/schools/overview')));
    _over = (jsonDecode(over!.body) as Map)['overview'] as Map<String, dynamic>;
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final prefs = Prefs(MemoryKeyStore());
    await t.runAsync(prefs.load);
    final auth = f.auth(also: (r) async => await also?.call(r) ?? await serve(r));
    final me = f.json('/v1/me');
    me['schools'] = true;
    me['areas'] = {...(me['areas'] as Map<String, dynamic>), 'schools': true};
    me['tenant_name'] = 'Bright Hill Academy';
    me['user'] = {...(me['user'] as Map<String, dynamic>), 'name': 'Chukwudi Ibe', 'email': 'ibe@brighthill.example'};
    auth.me = Me.fromJson(me);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: auth, prefs: prefs, now: () => f.at));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pump(const Duration(seconds: 1));
    return ChatsData(auth);
  }
}

/// The phone's own file chooser is not drawn: this stands in for what it hands the app (a CSV).
void pickCsv(String name) {
  final was = pickStatement;
  pickStatement = () async => (name: name, bytes: Uint8List.fromList(utf8.encode('Name,Admission number,Class,Guardian name,Guardian phone\nDayo Danjuma,P4/011,Primary 4,Mr Segun Danjuma,08037781204\n')));
  addTearDown(() => pickStatement = was);
}
