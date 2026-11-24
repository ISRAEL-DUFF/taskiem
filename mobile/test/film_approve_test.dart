// A film: approve a payment (Episode 10, "Keep it safe"). Dami has asked to
// pay Alhaja Sade; Zainab sees the Needs your OK card in Taskiem's own
// conversation (the card lives there, not on the Today tab's list), reads who
// asks, what, how much and to which account, taps Approve, reads the
// first-payment warning, adds a comment and approves. Set-up copied from
// taskiem_test.dart (openApproval); the screens are the app's own.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_approve_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/approve-a-payment --out approve.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/auth.dart' show Auth;
import 'package:taskiem/chat/approve.dart' show ApproveSheet;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

http.Response _ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});

Map<String, dynamic> _diary(String id, String text, String at) => {'id': id, 'channel': 'web', 'inbound': false, 'kind': 'diary', 'text': text, 'created_at': at};

final _diaryLines = {
  'messages': [
    _diary('d1', 'Sold 1 Ankara two-piece, 2 Aso oke gele: ₦96,000 (transfer)', '2026-11-11T09:12:00Z'),
    _diary('d2', 'Sold 1 Agbada set: ₦145,000 (transfer)', '2026-11-11T10:40:00Z'),
    _diary('d3', 'Sold 3 Ankara head ties: ₦21,000 (cash)', '2026-11-11T11:05:00Z'),
    _diary('d4', 'Sent invoice INV-0003 to Nneka Eze: ₦185,000', '2026-11-11T12:30:00Z'),
  ],
};

Map<String, dynamic> _alerts(bool open) => {
  'alerts': [
    if (open)
      {
        'key': 'approval:r1/ok',
        'kind': 'approval',
        'at': '2026-11-11T13:15:00Z',
        'title': 'Pay Alhaja Sade ₦480,000',
        'line': 'Asked by Dami · Pay a supplier',
        'id': 'r1',
        'approval': {
          'run_id': 'r1',
          'step_id': 'ok',
          'step_name': 'Pay Alhaja Sade ₦480,000',
          'workflow': 'Pay a supplier',
          'environment': 'prod',
          'role': 'owner',
          'required': 1,
          'approvals': 0,
          'level': 1,
          'levels': 1,
          'step_up': null,
          'policy': null,
          'sole_approver': true,
          'requested_at': '2026-11-11T13:15:00Z',
          'timeout_at': null,
          'subject': {'account_name': 'Alhaja Sade Fabrics', 'bank': 'Guaranty Trust Bank', 'account_number': '0123456789', 'amount_kobo': 48000000},
          'first_payment': true,
        },
        'who': 'Dami (apprentice)',
      },
  ],
};

/// The business and person in the story, in place of the recorded ones.
void zuri(Auth auth, Fixture f) {
  final j = f.json('/v1/me');
  j['tenant_name'] = 'Zuri Atelier';
  j['user'] = {...(j['user'] as Map<String, dynamic>), 'name': 'Zainab Yusuf', 'email': 'zainab@zuriatelier.example'};
  auth.me = Me.fromJson(j);
}

void main() {
  setUpAll(loadFonts);

  testWidgets('film: approve a payment', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    var open = true;
    final auth = f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path.startsWith('/v1/approvals/')) {
          open = false;
          return _ok({});
        }
        if (req.method != 'GET') return null;
        return switch (req.url.path) {
          '/v1/chat-alerts' => _ok(_alerts(open)),
          '/v1/taskiem-chat' => req.url.queryParameters.containsKey('after') ? _ok({'messages': []}) : _ok(_diaryLines),
          '/v1/chat-briefing' => _ok({'ready': false, 'day': '2026-11-11', 'key': 'briefing:2026-11-11', 'put_off': true, 'part': 'afternoon', 'now': '14:24'}),
          _ => null,
        };
      });
    zuri(auth, f);
    final chats = ChatsData(auth, now: () => f.at)..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(seconds: 1));

    final film = Film.start(t, 'approve-a-payment');
    film.say('Dami has asked to pay Alhaja Sade for fabric.', who: 'Zainab · Zuri Atelier');
    await film.play(2.4);

    final approve = find.bySemanticsLabel(RegExp('^Approve: Pay Alhaja'));
    film.say('The card says who asks, what for, how much, and where.');
    await t.ensureVisible(approve);
    await t.pump();
    await film.hold(3.6);

    film.say('Tap Approve to open the sheet with the details.');
    await film.tap(approve, then: 1.4);
    await film.settle();

    film.say('First payment to this account: check the details with her.');
    await film.hold(3.6);

    film.say('Add a comment. It is recorded with your decision.');
    final box = find.descendant(of: find.byType(ApproveSheet), matching: find.byType(EditableText));
    await film.tap(box.last, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(box.last, 'Checked the account with Alhaja Sade', perKey: 0.07);
    await film.hold(0.8);
    await film.keyboard(false);
    await film.hold(0.6);

    film.say('Tap Approve. The sheet shows Approved, then the card clears.');
    final go = find.descendant(of: find.byType(ApproveSheet), matching: find.bySemanticsLabel('Approve'));
    await film.tap(go, then: 0.5);
    await film.play(2.0);
    await film.settle();
    await film.hold(2.0);
    await film.finish();
  }, skip: !filming);
}
