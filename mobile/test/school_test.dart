// The school on the phone (the drawings' School tab, a student, money at
// the desk, chasing, the gate): what each screen shows from the server's
// answers and what it sends.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show ChatAlert, GetChatBriefingResponse;
import 'package:taskiem/chat/alerts.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/kit/mobile.dart' show TkScreen;
import 'package:taskiem/main.dart';
import 'package:taskiem/school/close_page.dart';
import 'package:taskiem/school/school_chasing.dart';
import 'package:taskiem/school/school_clearance.dart';
import 'package:taskiem/school/school_setup.dart';
import 'package:taskiem/school/school_tab.dart';
import 'package:taskiem/school/school_words.dart';
import 'package:taskiem/school/students_page.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'saved_stub.dart';
import 'school_fixture.dart';
import 'support.dart';

typedef Sent = ({String method, String path, Map<String, String> headers, Object? body});

void main() {
  setUpAll(loadFonts);

  test('the words: how late, which reminder, a hidden name', () {
    expect(rungFor(3), 1);
    expect(rungFor(12), 2);
    expect(rungFor(41), 3);
    expect(lateWords(1), '1 day late');
    expect(lateWords(41), '41 days late');
    expect(lateWords(0), 'not yet due');
    expect(nameOr({'name': 'A•••a O••', 'admission_number': 'JSS1A/014'}), 'JSS1A/014', reason: 'a masked name gives way to the admission number');
    expect(nameOr({'name': 'Amaka Obi', 'admission_number': 'JSS1A/014'}), 'Amaka Obi');
    expect(studentLine({'admission_number': 'JSS1A/014', 'class': 'JSS 1A'}), 'JSS1A/014 · JSS 1A');
  });

  Future<List<Sent>> open(WidgetTester t, Widget Function(ChatsData c) home, {Future<http.Response?> Function(http.Request r)? also}) async {
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <Sent>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method != 'GET') sent.add((method: req.method, path: req.url.path, headers: req.headers, body: req.body.isEmpty ? null : jsonDecode(req.body)));
        return await also?.call(req) ?? await schoolServer(req);
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('the School tab: the term, what came in, what is owed, where to go', (t) async {
    await open(t, (c) => SchoolTab(auth: c.auth));
    await looks(t, 'school');
    expect(find.text('FIRST TERM · 2026/2027'), findsOneWidget);
    expect(find.text('₦18,420,000'), findsOneWidget);
    expect(find.text('Tolu Adeyemi'), findsOneWidget, reason: 'owing the most');
    expect(find.text('Debtors and reminders'), findsOneWidget);
    await t.tap(find.text('Debtors and reminders'));
    await settle(t);
    await looks(t, 'school-debtors');
    expect(find.text('Debtors'), findsWidgets);
    expect(find.text('Kemi Bello'), findsOneWidget);
  });

  testWidgets('one student: what is owed, their account; cash recorded once with a key', (t) async {
    final sent = await open(t, (c) => StudentPage(auth: c.auth, id: 'S1'));
    expect(find.text('Amaka Obi'), findsWidgets);
    expect(find.text('₦96,000'), findsWidgets);
    expect(find.text('9012 345 678'), findsOneWidget);
    await t.tap(find.text('Record cash'));
    await settle(t);
    expect(find.text('Record a payment'), findsOneWidget);
    await t.enterText(find.byType(EditableText).first, '50000');
    await t.pump();
    await t.tap(find.text('Post the payment'));
    await settle(t);
    final post = sent.singleWhere((s) => s.path == '/v1/schools/payments');
    expect(post.headers['Idempotency-Key'] ?? post.headers['idempotency-key'], isNotEmpty);
    final body = post.body! as Map<String, dynamic>;
    expect(body['student'], 'S1');
    expect(body['amount_kobo'], 5000000);
    expect(body['how'], 'cash');
    await t.pump(const Duration(seconds: 6));
  });

  testWidgets('debtors: two chosen, the reminder their lateness calls for', (t) async {
    final sent = await open(t, (c) => DebtorsPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('Choose Tolu Adeyemi'));
    await t.tap(find.bySemanticsLabel('Choose Kemi Bello'));
    await t.pump();
    expect(find.text('2 families chosen'), findsOneWidget);
    await t.tap(find.text('Send'));
    await settle(t);
    await t.tap(find.text('Send to 2'));
    await settle(t);
    final post = sent.singleWhere((s) => s.path == '/v1/schools/reminders');
    final body = post.body! as Map<String, dynamic>;
    expect(body['students'], ['S2', 'S3']);
    expect(body['rung'], 3, reason: 'the latest of them is 41 days late');
    await t.pump(const Duration(seconds: 6));
  });

  testWidgets('the gate: a name, then CLEARED; never an amount', (t) async {
    await open(t, (c) => GateScreen(auth: c.auth, onSignOut: () {}));
    await t.enterText(find.byType(EditableText), 'ama');
    await t.pump(const Duration(milliseconds: 400));
    await settle(t);
    await t.tap(find.text('Amaka Obi'));
    await settle(t);
    expect(find.text('CLEARED'), findsOneWidget);
    expect(find.text('Amaka Obi · JSS 1A'), findsOneWidget);
    await t.tap(find.text('Check another child'));
    await settle(t);
    expect(find.text('CLEARED'), findsNothing);
    expect(find.text('Amaka Obi'), findsWidgets, reason: 'in the list just checked');
  });

  testWidgets('the gate will not show an answer carrying an amount', (t) async {
    await open(
      t,
      (c) => GateScreen(auth: c.auth, onSignOut: () {}),
      also: (r) async => r.url.path == '/v1/gate/students/S1'
          ? http.Response(jsonEncode({'student_id': 'S1', 'name': 'Amaka Obi', 'class': 'JSS 1A', 'status': 'cleared', 'balance_kobo': 9600000}), 200, headers: {'content-type': 'application/json'})
          : null,
    );
    await t.enterText(find.byType(EditableText), 'ama');
    await t.pump(const Duration(milliseconds: 400));
    await settle(t);
    await t.tap(find.text('Amaka Obi'));
    await settle(t);
    expect(find.text('CLEARED'), findsNothing);
    expect(find.textContaining('₦'), findsNothing);
  });

  testWidgets('the day’s close: each thing to fix in words, with Fix it', (t) async {
    await open(t, (c) => CloseDayPage(auth: c.auth, day: '2026-11-16'));
    expect(find.text('DOES NOT RECONCILE'), findsOneWidget);
    expect(find.text('Transfer from ADEBAYO O.'), findsOneWidget);
    expect(find.text('No student’s account; ref 118204'), findsOneWidget);
    expect(find.text('Fix it'), findsWidgets);
  });

  testWidgets('students: the list and its count', (t) async {
    await open(t, (c) => StudentsPage(auth: c.auth));
    expect(find.text('418 students'), findsOneWidget);
    expect(find.text('Dami Ogun'), findsOneWidget);
    expect(find.text('Withdrawn'), findsWidgets);
  });

  testWidgets('a student added: the guardian’s phone from a saved person', (t) async {
    final stub = SavedStub(mine: [savedThing('M1', 'mine', 'person', 'mrs-ogun', 'Mrs Ogun', '••4567', {'phone': '08031234567', 'email': 'ogun@example.com'})]);
    await open(t, (c) => StudentsPage(auth: c.auth), also: (r) async => stub.answer(r));
    await t.tap(find.bySemanticsLabel('Add a student'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Pick a saved phone number for the guardian'));
    await settle(t);
    expect(find.text('Saved phone numbers'), findsOneWidget);
    await t.tap(find.text('Mrs Ogun'));
    await settle(t);
    expect(find.text('08031234567'), findsOneWidget);
    expect(find.text('ogun@example.com'), findsNothing, reason: 'the phone only');
  });

  testWidgets('in the Taskiem chat: the school’s briefing, and cash waiting for this person', (t) async {
    final sent = await open(
      t,
      (c) => TkScreen(title: 'Taskiem', children: [
        BriefingCard(briefing: GetChatBriefingResponse.fromJson(schoolBriefing), auth: c.auth),
        AlertCards(alerts: [ChatAlert.fromJson(schoolApprovalAlert)], auth: c.auth, now: DateTime.utc(2026, 11, 17, 6, 5)),
      ]),
    );
    expect(find.text('Fees collected yesterday'), findsOneWidget);
    expect(find.text('₦612,000'), findsOneWidget);
    expect(find.text('71% of First term’s fees in · the close reconciles'), findsOneWidget);
    expect(find.text('341 of 418'), findsOneWidget);
    expect(find.text('Bisi asks: cash for JSS1A/014'), findsOneWidget);
    expect(find.text('₦50,000'), findsWidgets);
    await t.tap(find.text('Approve').last);
    await settle(t);
    expect(find.text('You are the second person'), findsOneWidget);
    await t.tap(find.text('Approve').last);
    await settle(t);
    final post = sent.singleWhere((s) => s.path == '/v1/schools/approvals/P1');
    expect((post.body! as Map<String, dynamic>)['decision'], 'approved');
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('a payment plan approved in fewer parts: the same total, on the family’s days', (t) async {
    final sent = await open(
      t,
      (c) => PlanSheet(auth: c.auth, plan: const {
        'id': 'PL', 'name': 'Tolu Adeyemi', 'class': 'JSS 2A', 'created_at': '2026-11-17T07:12:00Z',
        'instalments': [{'amount_kobo': 6150000, 'due_on': '2026-11-27'}, {'amount_kobo': 6150000, 'due_on': '2026-12-11'}, {'amount_kobo': 6150000, 'due_on': '2027-01-08'}],
      }),
    );
    expect(find.text('by Fri 11 Dec'), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp(r'^2$')).first);
    await t.pump();
    expect(find.text('₦92,250'), findsNWidgets(2));
    expect(find.text('by Fri 11 Dec'), findsNothing, reason: 'two parts: the first day and the last');
    await t.tap(find.text('Approve the plan'));
    await settle(t);
    final body = sent.singleWhere((s) => s.path == '/v1/schools/plans/PL').body! as Map<String, dynamic>;
    expect(body['decision'], 'approved');
    expect(body['instalments'], [{'amount_kobo': 9225000, 'due_on': '2026-11-27'}, {'amount_kobo': 9225000, 'due_on': '2027-01-08'}]);
    await t.pump(const Duration(seconds: 6));
  });

  testWidgets('a class’s fees: the classes across the top, an amount changed and saved', (t) async {
    final sent = await open(t, (c) => ClassFeesSheet(auth: c.auth, term: const {'id': 'T1', 'name': 'First term', 'state': 'current'}, termName: 'First term · 2026/2027'));
    expect(find.text('JSS 1A fees'), findsOneWidget);
    expect(find.text('Required · collected for WAEC'), findsOneWidget);
    expect(find.text('Every student pays ₦190,000, of which ₦15,000 is for WAEC; ₦35,000 more is optional.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).first, '160,000');
    await t.pump();
    await t.tap(find.text('Save JSS 1A’s fees'));
    await settle(t);
    final put = sent.singleWhere((s) => s.path == '/v1/schools/terms/T1/fees/c0');
    final items = (put.body! as Map<String, dynamic>)['items'] as List;
    expect(items.first, {'name': 'Tuition', 'amount_kobo': 16000000, 'kind': 'required'});
    expect(items.last, {'name': 'WAEC registration', 'amount_kobo': 1500000, 'kind': 'required', 'pass_through_for': 'WAEC'});
    await t.pump(const Duration(seconds: 6));
  });

  testWidgets('promotion: each class and where it goes, and the exceptions', (t) async {
    await open(t, (c) => PromotionPage(auth: c.auth, session: const {'id': 'SE', 'label': '2026/2027'}));
    expect(find.bySemanticsLabel('JSS 1A to JSS 2A, 32 students'), findsOneWidget);
    expect(find.bySemanticsLabel('SS 3A to Graduate, 24 students'), findsOneWidget);
    expect(find.text('Exceptions'), findsOneWidget);
    expect(find.textContaining('At the end of 2026/2027'), findsOneWidget);
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pumpAndSettle();
}
