// A film: a customer chats with a business (Episode 3 of "Taskiem for you").
// Tobi opens Ada's Kitchen's chat link in the app (its card), taps Chat with
// Ada's Kitchen, reads the first-chat note, answers the offers question,
// types a message and sends it; Ada's reply comes into the same chat.
// Set-up from my_chats_test.dart (chat link, conversation, writing).
// Skipped unless VIDEO_DIR is set.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/chat_link.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_you_ep34_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: chat with a business', (t) async {
    final api = YouApi();
    var messages = <Map<String, dynamic>>[];
    var chat = adaChat();
    api.routes['GET /v1/chat-links/ada4kitchen'] = (_) => {
      'name': 'Ada’s Kitchen',
      'tagline': 'Jollof rice, zobo and small chops',
      'area': 'Yaba, Lagos',
      'open_now': true,
      'hours': {
        'days': [
          for (var i = 0; i < 7; i++)
            [
              {'open': '08:00', 'close': i == 6 ? '16:00' : '20:00'},
            ],
        ],
      },
      'takes_chats': true,
      'code': 'ada4kitchen',
      'link': 'https://app.taskiem.com/m/ada4kitchen',
      'words': 'Chat with Ada’s Kitchen on Taskiem: https://app.taskiem.com/m/ada4kitchen',
    };
    api.routes['POST /v1/me/chats'] = (_) => answer({...chat, 'notice': 'Ada’s Kitchen will see your name and phone number, for your orders and deliveries. Messages with businesses can be read by Taskiem to help with your orders.'}, 201);
    api.routes['GET /v1/me/chats/c1'] = (_) => chat;
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {'messages': messages, 'more': false};
    api.routes['POST /v1/me/chats/c1/read'] = (_) => answer({}, 204);
    api.routes['POST /v1/me/chats/c1/typing'] = (_) => answer({}, 204);
    api.routes['PUT /v1/me/chats/c1'] = (_) {
      chat = adaChat(offers: 'unasked', ask: false);
      return chat;
    };
    api.routes['POST /v1/me/chats/c1/messages'] = (r) {
      final b = api.body(r);
      final m = textMsg('m1', true, b['text'] as String, '12:59');
      messages = [m];
      return answer({...m, 'read_at': null}, 201);
    };

    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    final auth = api.auth();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkScreen(auth: auth, code: 'ada4kitchen')));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'you-chat-business');
    film.say('Tobi opens Ada’s Kitchen’s chat link. It shows her card.', who: 'Tobi · Ada’s Kitchen');
    await film.hold(4.0);
    film.say('He taps Chat with Ada’s Kitchen.');
    await film.hold(1.0);
    await film.tap(find.text('Chat with Ada’s Kitchen'), then: 0.6);
    await film.settle();
    film.say('The chat opens, with a note on what Ada will see.');
    await film.hold(4.5);
    film.say('He taps OK to close the note.');
    await film.hold(1.0);
    await film.tap(find.text('OK'), then: 0.8);
    await film.settle();
    film.say('Ada asks about offers. He taps Not now.');
    await film.hold(3.0);
    await film.tap(find.bySemanticsLabel('Not now'), then: 0.8);
    await film.settle();
    film.say('He taps the message box and types a question.');
    final box = find.byType(EditableText);
    await film.hold(1.0);
    await film.tap(box, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(box, 'Good afternoon, do you have jollof rice today?', perKey: 0.07);
    await film.hold(0.8);
    film.say('He taps Send.');
    await film.hold(1.0);
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Send'), then: 0.6);
    await film.settle();
    await film.hold(1.5);

    messages = [
      textMsg('m1', true, 'Good afternoon, do you have jollof rice today?', '12:59'),
      textMsg('m2', false, 'Good afternoon Tobi! Yes, jollof rice is ₦3,500 a pack.', '13:00'),
      textMsg('m3', false, 'Zobo is ₦1,500. Tell me what you would like.', '13:00'),
    ];
    chat = adaChat(offers: 'unasked', ask: false, last: 'Zobo is ₦1,500. Tell me what you would like.');
    await t.runAsync(() => (t.state(find.byType(MyChatScreen)) as dynamic).widget.data.refresh() as Future<void>);
    await film.play(0.5);
    film.say('Ada’s reply arrives in the same chat, with her prices.');
    await film.play(4.5);
    await film.finish();
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/messages').single)['text'], 'Good afternoon, do you have jollof rice today?');
  }, skip: !filming);
}
