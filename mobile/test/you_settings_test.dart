// You's settings for the person's own account (docs/spec/taskiem-for-
// everyone.md §7, §14): the photo (only theirs; businesses never see it),
// which notifications businesses may send, the privacy page with Ask to
// erase, and deleting the account (a fresh proof, the refusals with the
// businesses named, a second proof first where asked, then signed out).

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/transport.dart' show ApiError;
import 'package:taskiem/app/auth.dart' show AuthState;
import 'package:taskiem/chat/attachments.dart' show PickedFile;
import 'package:taskiem/chat/composer.dart' show Pickers;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/screens/you_account.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'my_chats_test.dart' show Api, answer, at, chat, drain, phone, press, settle, typeIn;
import 'support.dart' show loadFonts;
import 'you_stage3_test.dart' show you;

class _Pick extends Pickers {
  const _Pick();
  @override
  Future<PickedFile?> photo({double? side}) async => PickedFile(bytes: Uint8List.fromList([1, 2, 3]), name: 'me.jpg', type: 'image/jpeg');
}

Map<String, dynamic> me({Object? photo}) => {
  'tenant_id': null,
  'personal': true,
  'roles': <String>[],
  'permissions': <String>[],
  'auth_method': 'phone',
  'user': {'id': 'u1', 'name': 'Tolu Ade', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230000', 'photo': photo},
  'businesses': <Object>[],
};

Future<void> settings(WidgetTester t, Api api, {Pickers pickers = const _Pick()}) async {
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouSettingsScreen(auth: api.auth(), pickers: pickers, now: () => at)));
  await settle(t);
}

void main() {
  setUpAll(loadFonts);

  testWidgets('the photo: added, only the person\'s, then removed', (t) async {
    phone(t);
    final api = you();
    Object? photo;
    api.routes['GET /v1/me'] = (_) => me(photo: photo);
    api.routes['PUT /v1/me/photo'] = (_) {
      photo = {'url': '/v1/me/photo?v=1', 'updated_at': '2026-11-17T10:00:00Z'};
      return {'photo': photo};
    };
    api.routes['GET /v1/me/photo'] = (_) => http.Response.bytes(base64Decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII='), 200, headers: {'content-type': 'image/jpeg'});
    api.routes['DELETE /v1/me/photo'] = (_) {
      photo = null;
      return answer({}, 204);
    };
    await settings(t, api);
    expect(find.text('Only you see your photo. Businesses never do.'), findsOneWidget);
    await press(t, find.text('Add a photo'));
    final put = api.sent('PUT', '/v1/me/photo').single;
    expect(put.headers['content-type'], startsWith('multipart/form-data'));
    expect(put.body, contains('name="file"; filename="me.jpg"'));
    expect(find.text('Your photo is saved'), findsOneWidget);
    expect(find.text('Change photo'), findsOneWidget);
    await press(t, find.text('Remove'));
    await press(t, find.text('Remove').last);
    expect(api.sent('DELETE', '/v1/me/photo'), hasLength(1));
    expect(find.text('Add a photo'), findsOneWidget);
    await drain(t);
  });

  testWidgets('notifications: what businesses may send, each turned at once and put back if refused', (t) async {
    phone(t);
    final api = you();
    var refuse = false;
    api.routes['GET /v1/me/notifications'] = (_) => {'messages': true, 'updates': true, 'offers': true, 'people': true, 'requests': true};
    api.routes['PUT /v1/me/notifications'] = (r) {
      if (refuse) return answer({'error': 'Something went wrong. Try again.'}, 500);
      return {'messages': true, 'updates': true, 'offers': api.body(r)['offers'] ?? true, 'people': api.body(r)['people'] ?? true, 'requests': true};
    };
    await settings(t, api);
    await press(t, find.text('Notifications'));
    expect(find.text('When someone at a business writes to you'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Offers').last);
    expect(api.body(api.sent('PUT', '/v1/me/notifications').single), {'offers': false});
    refuse = true;
    await press(t, find.bySemanticsLabel('Updates').last);
    expect(api.body(api.sent('PUT', '/v1/me/notifications').last), {'updates': false});
    expect(find.text('Something went wrong. Try again.'), findsOneWidget);
    expect(find.textContaining('every phone you use Taskiem on'), findsOneWidget);
    // People chats' two kinds (people-chatting §13).
    refuse = false;
    expect(find.text('Message requests'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Messages from people').last);
    expect(api.body(api.sent('PUT', '/v1/me/notifications').last), {'people': false});
    await drain(t);
  });

  testWidgets('privacy: what businesses see, which have the number, Ask to erase opens the chat', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/privacy'] = (_) => {
      'shown_to_businesses': {'name': 'Tolu Ade', 'phone': '+2348031230000', 'photo': false},
      'businesses': [
        {'business_id': 'b1', 'business': 'Mama’s Kitchen', 'chat_id': 'c1', 'orders': 2, 'last_order_at': '2026-11-15T10:00:00Z'},
        {'business_id': 'b2', 'business': 'Bola Stores', 'chat_id': null, 'orders': 1, 'last_order_at': '2026-10-01T10:00:00Z'},
      ],
      'erase': {'words': 'Each business keeps its own records. Ask each one in its chat to erase your details.', 'how': 'ask_in_chat'},
    };
    api.routes['GET /v1/me/chats/c1'] = (_) => chat(offers: 'no', ask: false);
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {'messages': <Object>[], 'more': false};
    api.routes['POST /v1/me/chats/c1/read'] = (_) => answer({}, 204);
    api.routes['POST /v1/me/chats'] = (_) => chat(id: 'c1', offers: 'no', ask: false);
    await settings(t, api);
    await press(t, find.text('Privacy'));
    expect(find.text('Never. Only you see it.'), findsOneWidget);
    expect(find.text('2 orders · last 15 Nov'), findsOneWidget);
    expect(find.text('Each business keeps its own records. Ask each one in its chat to erase your details.'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Ask Mama’s Kitchen to erase your details'));
    expect(api.sent('POST', '/v1/me/chats'), isEmpty, reason: 'the chat they have');
    expect((t.widget(find.byType(EditableText)) as EditableText).controller.text, eraseDraft);
    t.state<NavigatorState>(find.byType(Navigator).first).pop();
    await settle(t);
    await press(t, find.bySemanticsLabel('Ask Bola Stores to erase your details'));
    expect(api.body(api.sent('POST', '/v1/me/chats').single), {'business_id': 'b2', 'from': 'order'});
    await drain(t);
  });

  testWidgets('delete: a code to the phone, then deleted and signed out', (t) async {
    phone(t);
    final api = you();
    api.routes['POST /v1/me/delete/start'] = (_) => answer({'sent_via': 'whatsapp', 'expires_in': 600}, 202);
    var wrong = true;
    api.routes['POST /v1/me/delete'] = (_) {
      if (wrong) {
        wrong = false;
        return answer({'error': 'That code is not right.', 'code_wrong': true}, 401);
      }
      return {'deleted': true, 'businesses_told': 2};
    };
    final auth = api.auth();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: DeleteAccountScreen(auth: auth)));
    await settle(t);
    expect(find.textContaining('This customer left Taskiem'), findsOneWidget);
    await press(t, find.textContaining('Send a code to'));
    expect(api.body(api.sent('POST', '/v1/me/delete/start').single), <String, dynamic>{});
    expect(find.textContaining('on WhatsApp'), findsOneWidget);
    await typeIn(t, find.byKey(const ValueKey('delete-code')), '123456');
    await press(t, find.text('Delete my account'));
    await press(t, find.text('Delete my account').last);
    expect(api.body(api.sent('POST', '/v1/me/delete').single), {'confirm': true, 'code': '123456'});
    expect(find.text('That code is not right.'), findsOneWidget);
    expect(auth.state, AuthState.signedIn);
    await press(t, find.text('Delete my account'));
    await press(t, find.text('Delete my account').last);
    expect(api.sent('POST', '/v1/me/delete'), hasLength(2));
    expect(auth.state, AuthState.signedOut, reason: 'signed out, the phone forgets it all');
    expect(auth.transport.token, isNull);
    await drain(t);
  });

  testWidgets('delete: an owner is told plainly, the businesses named', (t) async {
    phone(t);
    final api = you();
    api.routes['POST /v1/me/delete/start'] = (_) => answer({
      'owns_business': true,
      'businesses': ['Mama’s Kitchen', 'Bola Stores'],
      'error': 'You own a business on Taskiem.',
    }, 409);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: DeleteAccountScreen(auth: api.auth())));
    await settle(t);
    await press(t, find.textContaining('Send a code to'));
    expect(
      find.text('You own Mama’s Kitchen and Bola Stores on Taskiem. Close it, or make someone else its owner and ask them to remove you, before you delete your account.'),
      findsOneWidget,
    );
    expect(find.text('Delete my account'), findsNothing);
    expect(deleteProblem(ApiError(409, {'in_business': true, 'businesses': ['Tunde Kicks']})), 'You are part of Tunde Kicks on Taskiem. Ask its owner to remove you, then delete your account.');
    await drain(t);
  });

  testWidgets('delete with the password on a new phone: the second proof first', (t) async {
    phone(t);
    final api = you();
    api.routes['POST /v1/me/delete'] = (_) => answer({
      'error': 'Prove it is you on this phone first.',
      'second_proof_required': true,
      'ways': {'password': true},
    }, 401);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: DeleteAccountScreen(auth: api.auth())));
    await settle(t);
    await press(t, find.text('Use my password'));
    await typeIn(t, find.byKey(const ValueKey('delete-password')), 'hunter22');
    await press(t, find.text('Delete my account'));
    await press(t, find.text('Delete my account').last);
    expect(api.body(api.sent('POST', '/v1/me/delete').single), {'confirm': true, 'password': 'hunter22'});
    expect(find.text('Prove it\'s you'), findsOneWidget);
    await drain(t);
  });

  testWidgets('You\'s settings can be pressed: words, actions, 48 by 48', (t) async {
    phone(t);
    final api = you();
    await settings(t, api);
    await expectPressable(t);
    await drain(t);
  });
}
