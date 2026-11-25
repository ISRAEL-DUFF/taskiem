// A film: keep it safe (customers, Episode 10). Tobi mutes a chat, reads what
// Taskiem says about where chats are kept (not end-to-end encrypted), blocks
// someone, then in Settings sees Blocked people, Privacy (what businesses see)
// and the Delete your account page (read, not pressed). Set-up from
// people_test.dart (a person's chat in You), you_settings_test.dart (Privacy,
// Delete) and people_test.dart's Blocked people. Skipped unless VIDEO_DIR is set.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_you_keep_safe_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_you_ep910_world.dart';
import 'my_chats_test.dart' show answer;
import 'people_test.dart' show peopleChat;
import 'support.dart' show loadFonts;

void main() {
  setUpAll(loadFonts);

  testWidgets('film: you keep safe', (t) async {
    final api = tobiApi();
    youRoutes(api, group: false, person: true);
    var muted = false;
    var blocked = false;
    Map<String, dynamic> chatJson() => {...peopleChat(name: 'Dapo Ige', phone: '+2348031234567', blocked: blocked), 'muted': muted};
    api.routes['GET /v1/me/people/pc1'] = (_) => chatJson();
    api.routes['GET /v1/me/people/pc1/messages'] = (_) => {
      'messages': [
        {'id': 'm1', 'mine': false, 'kind': 'text', 'body': 'Did you see the match?', 'read_at': null, 'created_at': '2026-11-24T11:50:00Z'},
        {'id': 'm2', 'mine': false, 'kind': 'text', 'body': 'Send me ₦5,000 for the ticket', 'read_at': null, 'created_at': '2026-11-24T11:52:00Z'},
      ],
      'more': false,
    };
    api.routes['POST /v1/me/people/pc1/read'] = (_) => answer({}, 204);
    api.routes['PUT /v1/me/people/pc1'] = (r) {
      muted = api.body(r)['muted'] ?? muted;
      return chatJson();
    };
    api.routes['POST /v1/me/people/pc1/block'] = (_) {
      blocked = true;
      return {'blocked': true, 'removed': false, 'reported': false};
    };
    api.routes['GET /v1/me/people/blocked'] = (_) => {
      'people': [
        if (blocked) {'person_id': 'p1', 'name': 'Dapo Ige', 'chat_id': 'pc1', 'at': '2026-11-24T12:40:00Z'},
      ],
    };
    api.routes['GET /v1/me/privacy'] = (_) => {
      'shown_to_businesses': {'name': 'Tobi Adewale', 'phone': '+2348031230000', 'photo': false},
      'businesses': [
        {'business_id': 'ada', 'business': 'Ada’s Kitchen', 'chat_id': 'c1', 'orders': 3, 'last_order_at': '2026-11-23T14:00:00Z'},
      ],
      'erase': {'words': 'Each business keeps its own records. Ask each one in its chat to erase your details.', 'how': 'ask_in_chat'},
    };
    final auth = tobiAuth(api);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: auth, now: () => tobiNow, presence: false)));
    await _settle(t);

    final film = Film.start(t, 'you-keep-safe');
    film.say('Tobi gets a message he does not like. Open the chat.', who: 'Tobi · his phone');
    await film.hold(3.2);
    await film.tap(find.text('Dapo Ige'), then: 1.4);
    await _settle(t);
    film.say('Tap More, then Mute notifications.');
    await film.hold(2.6);
    await film.tap(find.bySemanticsLabel('More about this conversation'), then: 1.0);
    await film.hold(1.0);
    await film.tap(find.text('Mute notifications'), then: 0.8);
    await _settle(t);
    film.say('The chat is muted. No more alerts from it.');
    await film.hold(2.6);
    film.say('Tap More again, then About Dapo Ige.');
    await film.tap(find.bySemanticsLabel('More about this conversation'), then: 1.0);
    await film.hold(1.0);
    await film.tap(find.text('About Dapo Ige'), then: 1.2);
    await _settle(t);
    film.say('Taskiem keeps chats on its servers. They are not end-to-end encrypted.');
    await t.ensureVisible(find.textContaining('not end-to-end encrypted'));
    await t.pump();
    await film.hold(6.0);
    film.say('To stop them writing, tap Block Dapo Ige.');
    await film.hold(1.0);
    await film.tap(find.text('Block Dapo Ige'), then: 1.2);
    await film.hold(2.6);
    film.say('Tap Block. They are not told. You can unblock later.');
    await film.tap(find.text('Block').last, then: 1.0);
    await _settle(t);
    await film.hold(2.4);
    film.say('Go back to Chats. Tap More, then Settings.');
    await film.tap(find.bySemanticsLabel(RegExp('^Back')).last, then: 0.8);
    await film.tap(find.bySemanticsLabel(RegExp('^Back')).last, then: 0.8);
    await _settle(t);
    await film.hold(1.2);
    await film.tap(find.bySemanticsLabel('More').last, then: 1.0);
    await film.hold(1.0);
    await film.tap(find.text('Settings'), then: 1.2);
    await _settle(t);
    film.say('In Settings, Blocked people lists who you blocked.');
    await t.ensureVisible(find.text('Blocked people'));
    await t.pump();
    await film.hold(2.6);
    await film.tap(find.text('Blocked people'), then: 1.2);
    await _settle(t);
    await film.hold(2.6);
    await film.tap(find.bySemanticsLabel(RegExp('^Back')).last, then: 0.8);
    await _settle(t);
    film.say('Privacy shows what a business sees: your name and number.');
    await t.ensureVisible(find.text('Privacy'));
    await t.pump();
    await film.hold(2.4);
    await film.tap(find.text('Privacy'), then: 1.2);
    await _settle(t);
    await film.hold(4.2);
    await film.tap(find.bySemanticsLabel(RegExp('^Back')).last, then: 0.8);
    await _settle(t);
    film.say('Delete your account is at the bottom of Settings.');
    await t.ensureVisible(find.text('Delete your account'));
    await t.pump();
    await film.hold(2.4);
    await film.tap(find.text('Delete your account'), then: 1.2);
    await _settle(t);
    film.say('It says what goes and what businesses keep. Nothing is deleted yet.');
    await film.hold(7.0);
    await film.finish();
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 6));
  }, skip: !filming);
}

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
