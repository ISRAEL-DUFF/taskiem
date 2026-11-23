// The team (web Staff.tsx, StaffPerson.tsx, Tasks.tsx; lib/team.ts,
// lib/tasks.ts): what adding, removing, a person's profile and the task
// steps send, and what each screen says.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter/services.dart';
import 'package:taskiem/chat/pick_problem.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/team_pages.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'guidelines.dart';
import 'saved_stub.dart';
import 'support.dart';

final _now = DateTime.utc(2026, 11, 12, 12);

final _staff = {
  'people': [
    {
      'user_id': 'U1',
      'name': 'Ada Obi',
      'email': 'ada@demo.test',
      'roles': ['owner'],
      'chat': true,
      'you': true,
    },
    {
      'user_id': 'U2',
      'name': 'Tunde',
      'phone': '+2348031110000',
      'roles': ['cashier'],
      'chat': true,
    },
  ],
  'invites': [
    {
      'id': 'I1',
      'name': 'Bisi',
      'phone': '+2348032220000',
      'roles': ['shift_manager'],
      'inviter': 'Ada',
      'sent_at': '2026-11-11T09:00:00Z',
      'expires_at': '2026-11-18T09:00:00Z',
      'created_at': '2026-11-11T09:00:00Z',
    },
  ],
};

final _clock = {
  'day': '2026-11-12',
  'shifts': [
    {'id': 'S1', 'user_id': 'U2', 'name': 'Tunde', 'in_at': '2026-11-12T07:25:00Z'},
  ],
  'in': [
    {'id': 'S1'},
  ],
  'late': [
    {'user_id': 'U3', 'name': 'Chidi', 'due': '08:00'},
  ],
};

final _person = {
  'user_id': 'U2',
  'name': 'Tunde',
  'phone': '+2348031110000',
  'roles': ['cashier'],
  'chat': true,
  'profile': {
    'title': 'Cashier',
    'started_on': '2026-01-05',
    'note': '',
    'pay_kobo': 8000000,
    'pay_per': 'month',
    'rota': [
      {'day': 'mon', 'start': '08:00', 'end': '16:00'},
      {'day': 'tue', 'start': '08:00', 'end': '16:00'},
      {'day': 'sat', 'start': '10:00', 'end': '18:00'},
    ],
  },
};

final _figures = {
  'period': 'week',
  'minutes': 485,
  'shifts': 1,
  'late_days': ['2026-11-10'],
  'missed_days': [],
  'sales': {
    'count': 4,
    'total_kobo': 1200000,
    'by_how': {'cash': 700000, 'transfer': 500000},
  },
  'handovers': [
    {'at': '2026-11-12T16:00:00Z', 'diff_kobo': -50000},
  ],
  'orders': 2,
  'tasks': {'done': 3, 'done_late': 1, 'cant': 0, 'open': 1, 'late': 0},
};

Map<String, dynamic> _tasks({bool give = true}) => {
  'give': give,
  'people': [
    {
      'id': 'U2',
      'name': 'Tunde',
      'roles': ['cashier'],
    },
  ],
  'tasks': [
    {
      'id': 'T1',
      'what': 'Count the cash float',
      'given_by': 'U1',
      'giver': 'Ada',
      'to_user': 'U1',
      'person': 'Ada',
      'due_at': '2026-11-12T15:00:00Z',
      'photo': false,
      'state': 'open',
      'created_at': '2026-11-12T08:00:00Z',
      'late': false,
    },
    {
      'id': 'T2',
      'what': 'Sweep the shop',
      'given_by': 'U1',
      'giver': 'Ada',
      'to_role': 'cashier',
      'photo': true,
      'state': 'open',
      'created_at': '2026-11-12T08:00:00Z',
      'late': true,
    },
  ],
  'repeats': [
    {'id': 'R1', 'what': 'Open the shutters', 'to_role': 'cashier', 'photo': false, 'every': 'day', 'at': '07:00'},
  ],
};

void main() {
  setUpAll(loadFonts);

  test('the team and tasks in words', () {
    expect(staffProblem('', '0803', 'cashier'), 'Give their name.');
    expect(staffProblem('Bisi', '0803', 'cashier'), 'Give their mobile number, like 0803 123 4567.');
    expect(staffProblem('Bisi', '0803 123 4567', 'cashier'), isNull);
    expect(personSub(_staff['people']![1] as Map<String, dynamic>), 'Cashier · +2348031110000');
    expect(rotaWords((_person['profile']! as Map)['rota'] as List<Map<String, dynamic>>), 'Mon to Tue, 08:00 to 16:00; Sat, 10:00 to 18:00');
    expect(payWords(_person['profile']! as Map<String, dynamic>), '₦80,000 a month');
    expect(lengthWords(const Duration(minutes: 485)), '8 hours 5 minutes');
    expect(shiftWords({'in_at': '2026-11-12T07:25:00Z'}), 'Since 08:25');
    expect(
      shiftWords({'in_at': '2026-11-12T07:25:00Z', 'out_at': '2026-11-12T15:30:00Z', 'out_how': 'handover'}),
      '08:25 to 16:30 · 8 hours 5 minutes · handed over',
    );
    final rows = figureRows(_figures);
    expect(rows, contains(('Sales', '₦12,000 in 4 sales (Cash ₦7,000, Transfer ₦5,000)')));
    expect(rows, contains(('Handovers', '1, ₦500 short')));
    expect(rows, contains(('Time keeping', 'Late on 1 day')));
    expect(rows.last, ('Tasks', '3 done (1 late), 1 open'));
    final t2 = (_tasks()['tasks'] as List)[1] as Map<String, dynamic>;
    expect(taskWho(t2), 'any cashier');
    expect(taskSub(t2, true, _now), 'for any cashier · photo when done · from Ada');
    expect(dueWords('2026-11-12T15:00:00Z', _now), 'by 16:00');
    expect(dueWords('2026-11-13T15:00:00Z', _now), 'by tomorrow 16:00');
    expect(taskBadge(t2)?.text, 'Late');
    expect(repeatWords({'every': 'week', 'day': 'mon', 'at': '09:00'}), 'Every Monday at 09:00');
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData c) home, {SavedStub? stub}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 2600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (stub?.answer(req) case final r?) return r;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/staff' => ok(_staff),
              '/v1/staff/clock' => ok(_clock),
              '/v1/roles' => ok({
                'roles': [
                  {'name': 'owner'},
                  {'name': 'cashier'},
                  {'name': 'packer'},
                ],
              }),
              '/v1/staff/U2' => ok(_person),
              '/v1/staff/U2/figures' => ok({..._figures, 'period': req.url.queryParameters['period']}),
              '/v1/staff/tasks' => ok(_tasks()),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          if (p == '/v1/staff/invites') return ok({'id': 'I9', 'sent': true});
          if (p.endsWith('/send')) return ok({'sent': true});
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('staff: who is in, who is late; someone added with their own role; an invitation sent again', (t) async {
    final sent = await open(t, (c) => StaffPage(auth: c.auth));
    await looks(t, 'team');
    expect(find.textContaining('in since 08:25'), findsOneWidget);
    expect(find.text('Chidi is late'), findsOneWidget);
    expect(find.text('Ada Obi (you)'), findsOneWidget);
    expect(find.bySemanticsLabel('Remove Ada Obi'), findsNothing, reason: 'not yourself');
    await t.tap(find.bySemanticsLabel('Send again to Bisi'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/invites/I1/send {}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Add staff'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Their name')), matching: find.byType(EditableText)), 'Kemi');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Their mobile number')), matching: find.byType(EditableText)), '0803 555 1234');
    await t.tap(find.bySemanticsLabel('What they do: Cashier'));
    await t.pumpAndSettle();
    await t.tap(find.text('Packer'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Send the invitation'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/staff/invites {"name":"Kemi","phone":"0803 555 1234","roles":["packer"]}');
    await t.pump(const Duration(seconds: 5));
    // Removed from their own screen, after yes.
    await t.tap(find.text('Tunde').last);
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Remove Tunde')));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Remove').last);
    await settle(t);
    expect(sent.last, 'POST /v1/staff/U2/remove {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('staff: their number from a saved place’s phone', (t) async {
    final stub = SavedStub(business: [savedThing('B1', 'business', 'place', 'kitchen', 'The kitchen', 'Yaba', {'line': '3 Herbert Macaulay Way', 'phone': '08035551234'})]);
    await open(t, (c) => StaffPage(auth: c.auth), stub: stub);
    await t.tap(find.bySemanticsLabel('Add staff'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Pick a saved phone number'));
    await t.pumpAndSettle();
    expect(find.text('Saved phone numbers'), findsOneWidget);
    expect(find.text('@kitchen · Yaba'), findsOneWidget);
    await t.tap(find.text('The kitchen'));
    await t.pumpAndSettle();
    expect(find.text('08035551234'), findsOneWidget);
  });

  testWidgets('a person: their hours and pay; this month’s figures; a Saturday taken off', (t) async {
    final sent = await open(t, (c) => StaffPersonPage(auth: c.auth, user: 'U2'));
    expect(find.text('Mon to Tue, 08:00 to 16:00; Sat, 10:00 to 18:00'), findsOneWidget);
    expect(find.text('₦80,000 a month · for your records'), findsOneWidget);
    expect(find.text('₦12,000'), findsOneWidget);
    expect(find.text('4 sales · 2 orders'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('This month'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('More about Tunde'));
    await t.pumpAndSettle();
    await t.tap(find.text('Change their job, hours and pay'));
    await t.pumpAndSettle();
    await t.tap(find.text('Saturday'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    await t.pumpAndSettle();
    expect(
      sent.last,
      'PUT /v1/staff/U2/profile {"title":"Cashier","started_on":"2026-01-05","note":"","pay_kobo":8000000,"pay_per":"month","rota":[{"day":"mon","start":"08:00","end":"16:00"},{"day":"tue","start":"08:00","end":"16:00"}]}',
    );
    await t.pump(const Duration(seconds: 5));
  });

  /// A staff member's page at 360 wide, with [account] linked (or none)
  /// and the business's saved bank accounts; PUT …/pay-account asks for
  /// a passkey first, then links what it was sent. [owner]: an owner
  /// looking; [pay]: with money.pay.
  Future<({List<String> sent, FakePasskeys keys})> openPerson(WidgetTester t, {Map<String, dynamic>? account, bool owner = true, bool pay = true}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(360, 1300);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final was = passkeys;
    final keys = FakePasskeys();
    passkeys = keys;
    addTearDown(() => passkeys = was);
    final f = Fixture('chats');
    final sent = <String>[];
    var linked = account;
    final stub = SavedStub(
      business: [
        savedThing('A1', 'business', 'bank_account', 'tunde_bank', 'Tunde (Moniepoint)', 'Moniepoint ••4821', {'bank_name': 'Moniepoint', 'account_number': '••4821'}, accountName: 'TUNDE BAKARE'),
        savedThing('A2', 'business', 'bank_account', 'tunde_gtb', 'Tunde (GTBank)', 'Guaranty Trust Bank ••1190', {'bank_name': 'Guaranty Trust Bank', 'account_number': '••1190'}),
      ],
    );
    http.Response ok(Object? b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (stub.answer(req) case final r?) return r;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/staff/U2' => ok({..._person, 'pay_account': linked}),
              '/v1/staff/U2/figures' => ok({..._figures, 'period': req.url.queryParameters['period']}),
              '/v1/payments/banks' => ok({'banks': <Object>[], 'from': 'test'}),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          if (p == '/v1/me/step-up/options') return ok({'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'}});
          if (p == '/v1/staff/U2/pay-account') {
            final b = jsonDecode(req.body) as Map<String, dynamic>;
            if (b['passkey'] == null) {
              return ok({'error': 'confirm with your passkey or authenticator code', 'step_up': 'required', 'methods': ['passkey'], 'operation': 'staff.account', 'target': 'U2'}, 403);
            }
            linked = b['saved_id'] == 'A1'
                ? {'saved_id': 'A1', 'name': 'tunde_bank', 'title': 'Tunde (Moniepoint)', 'bank_name': 'Moniepoint', 'last4': '4821', 'account_name': 'TUNDE BAKARE', 'linked_by': 'Ada Obi', 'linked_at': '2026-11-22T09:00:00Z'}
                : null;
            return ok({'pay_account': linked});
          }
          if (p == '/v1/payments') return ok({'error': 'stop here', 'code': 'no_provider'}, 409);
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    final me = chats.auth.me!.toJson();
    chats.auth.me = Me.fromJson({
      ...me,
      'roles': [if (owner) 'owner' else 'shift_manager'],
      'permissions': [...(me['permissions'] as List).where((x) => x != 'money.pay'), if (pay) 'money.pay'],
    });
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: StaffPersonPage(auth: chats.auth, user: 'U2')));
    await settle(t);
    return (sent: sent, keys: keys);
  }

  final linkedAccount = {'saved_id': 'A1', 'name': 'tunde_bank', 'title': 'Tunde (Moniepoint)', 'bank_name': 'Moniepoint', 'last4': '4821', 'account_name': 'TUNDE BAKARE'};

  testWidgets('where we pay them: none linked; an owner links a saved account with a passkey, then unlinks it', (t) async {
    final (:sent, :keys) = await openPerson(t);
    await t.drag(find.byType(Scrollable).first, const Offset(0, -2000));
    await settle(t);
    expect(find.text('Where we pay them'), findsOneWidget);
    expect(find.text('No account linked'), findsOneWidget);
    expect(find.text('Paid through Payrolla, not here'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Pay Tunde')), findsNothing, reason: 'nowhere to pay them');
    await looks(t, 'staff-pay-none');
    await t.tap(find.bySemanticsLabel(RegExp('^Link an account for Tunde')));
    await t.pumpAndSettle();
    expect(find.text('Where we pay Tunde'), findsOneWidget);
    await snap(t, 'staff-pay-pick');
    await t.tap(find.text('Tunde (Moniepoint)'));
    await t.pumpAndSettle();
    expect(find.text('Pay Tunde here from now on?'), findsOneWidget);
    await snap(t, 'staff-pay-sure');
    await t.tap(find.text('Link it'));
    await settle(t);
    final puts = sent.where((s) => s.startsWith('PUT /v1/staff/U2/pay-account')).toList();
    expect(puts, hasLength(2), reason: 'asked first, then with the passkey');
    expect(puts.first, 'PUT /v1/staff/U2/pay-account {"saved_id":"A1"}');
    expect(jsonDecode(puts.last.split(' ').skip(2).join(' ')), {'saved_id': 'A1', 'passkey': isA<Map<String, dynamic>>()});
    expect(sent, contains('POST /v1/me/step-up/options {"operation":"staff.account","target":"U2"}'));
    expect(keys.asked, hasLength(1));
    await t.pumpAndSettle();
    expect(find.text('Tunde (Moniepoint)'), findsOneWidget);
    expect(find.text('Moniepoint ••4821 · TUNDE BAKARE'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Pay Tunde')), findsOneWidget);
    // Unlinked, after yes: saved_id null.
    await t.tap(find.bySemanticsLabel(RegExp('^Unlink Tunde’s account')));
    await t.pumpAndSettle();
    await t.tap(find.text('Unlink').last);
    await settle(t);
    expect(sent.where((s) => s.startsWith('PUT /v1/staff/U2/pay-account')).last, contains('"passkey"'));
    await t.pumpAndSettle();
    expect(find.text('No account linked'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where we pay them: linked; Pay opens the Pay sheet to the staff member, for wages; not an owner: no Link', (t) async {
    final (:sent, keys: _) = await openPerson(t, account: linkedAccount, owner: false);
    await t.drag(find.byType(Scrollable).first, const Offset(0, -2000));
    await settle(t);
    expect(find.text('Moniepoint ••4821 · TUNDE BAKARE'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Change where Tunde is paid')), findsNothing, reason: 'owners only');
    expect(find.bySemanticsLabel(RegExp('^Unlink Tunde’s account')), findsNothing);
    await t.tap(find.bySemanticsLabel(RegExp('^Pay Tunde')));
    await settle(t);
    expect(find.text('Pay Tunde'), findsWidgets);
    await snap(t, 'staff-pay-sheet');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^How much')), matching: find.byType(EditableText)), '20000');
    await settle(t);
    await t.tap(find.text('Next'));
    await settle(t);
    final body = jsonDecode(sent.firstWhere((s) => s.startsWith('POST /v1/payments ')).substring('POST /v1/payments '.length)) as Map<String, dynamic>;
    expect(body['staff_id'], 'U2');
    expect(body['purpose'], 'wages');
    expect(body['amount_kobo'], 2000000);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where we pay them, an owner, linked: pictured', (t) async {
    await openPerson(t, account: linkedAccount);
    await t.drag(find.byType(Scrollable).first, const Offset(0, -2000));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('^Change where Tunde is paid')), findsOneWidget);
    await looks(t, 'staff-pay-linked');
  });

  testWidgets('tasks: done; taken from a role and done with a photo; can’t with why; everyone’s, reminded and stopped', (t) async {
    final was = pickTaskPhoto;
    pickTaskPhoto = () async => (bytes: [1, 2, 3], name: 'shop.jpg');
    addTearDown(() => pickTaskPhoto = was);
    final sent = await open(t, (c) => TasksPage(auth: c.auth, now: () => _now));
    expect(find.textContaining('by 16:00'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Done: Count the cash float'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/tasks/T1/done {}');
    expect(find.text('Done. Thank you.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('I’ll do it: Sweep the shop'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/tasks/T2/take {}');
    await t.tap(find.text('Sweep the shop'));
    await t.pumpAndSettle();
    await t.tap(find.text('Done, with a photo'));
    await settle(t);
    expect(sent.last, startsWith('POST /v1/staff/tasks/T2/done '), reason: 'the photo sent with it');
    // Can't is on the task, pressed.
    await t.tap(find.text('Count the cash float'));
    await t.pumpAndSettle();
    await t.tap(find.text('Can’t do it'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Why')), matching: find.byType(EditableText)), 'No float today');
    await t.tap(find.bySemanticsLabel('Send it back'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/staff/tasks/T1/cant {"why":"No float today"}');
    await t.tap(find.bySemanticsLabel('Everyone’s'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Remind any cashier: Sweep the shop'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/tasks/T2/remind {}');
    await t.tap(find.bySemanticsLabel('Repeating'));
    await settle(t);
    expect(find.text('Open the shutters'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Stop: Open the shutters'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Stop it'));
    await settle(t);
    expect(sent.last, 'DELETE /v1/staff/task-repeats/R1 ');
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/staff' => ok(_staff),
          '/v1/staff/clock' => ok(_clock),
          '/v1/staff/U2' => ok(_person),
          '/v1/staff/U2/figures' => ok(_figures),
          '/v1/staff/tasks' => ok(_tasks()),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: StaffPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: StaffPersonPage(auth: chats.auth, user: 'U2'),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: TasksPage(auth: chats.auth, now: () => _now),
      ),
      drive: settle,
    );
  });

  testWidgets('a task done with a photo, the camera refused: said with Open settings, and the task stays to do', (t) async {
    final was = pickTaskPhoto;
    pickTaskPhoto = () async => throw PlatformException(code: 'camera_access_denied');
    addTearDown(() => pickTaskPhoto = was);
    final settingsWas = openPhoneSettings;
    var opened = 0;
    openPhoneSettings = () async => opened++;
    addTearDown(() => openPhoneSettings = settingsWas);
    final sent = await open(t, (c) => TasksPage(auth: c.auth, now: () => _now));
    await t.tap(find.bySemanticsLabel('I’ll do it: Sweep the shop'));
    await settle(t);
    await t.tap(find.text('Sweep the shop'));
    await t.pumpAndSettle();
    await t.tap(find.text('Done, with a photo'));
    await settle(t);
    expect(sent.where((s) => s.startsWith('POST /v1/staff/tasks/T2/done')), isEmpty, reason: 'not done without its photo');
    expect(find.textContaining('Taskiem isn’t allowed to use your camera'), findsOneWidget);
    await t.tap(find.text('Open settings'));
    await t.pump();
    expect(opened, 1);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
