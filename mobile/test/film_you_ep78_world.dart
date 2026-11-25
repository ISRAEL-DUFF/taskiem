// The set-up the customer series' episodes 7 and 8 share (you-share,
// you-groups): Tobi Adewale's phone (a personal account) served by a
// stand-in server, as the app's own tests do (people_test.dart,
// people_share_gaps_test.dart, groups_test.dart). Not a test.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'people_test.dart' show Api, card;

/// The phone's clock in the films: Tuesday 24 November 2026, 13:00.
final DateTime filmNow = DateTime.utc(2026, 11, 24, 13, 0);

/// An [Api]'s account as [name] (the app's tests sign in as Tolu Ade).
Auth signedInAs(Api api, {required String name, required String phone}) {
  final a = api.auth();
  a.me = Me.fromJson({
    'tenant_id': null,
    'personal': true,
    'roles': <String>[],
    'permissions': <String>[],
    'auth_method': 'phone',
    'user': {'id': 'u1', 'name': name, 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': phone},
    'businesses': <Object>[],
  });
  a.state = AuthState.signedIn;
  return a;
}

/// A chat with a person, as a row of the one Chats list.
Map<String, dynamic> personRow({required String id, required String personId, required String name, String last = '', String at = '2026-11-24T11:50:00Z', bool request = false}) => {
  'kind': 'person',
  'id': id,
  'person_id': personId,
  'name': name,
  'gone': false,
  'last': last,
  'last_mine': false,
  'last_at': at,
  'unread': 0,
  'muted': false,
  'archived': false,
  'request': request,
  'waiting': false,
  'blocked': false,
  'person': card(id: personId, name: name),
};

Map<String, dynamic> chatsList(List<Map<String, dynamic>> chats, {int requests = 0, int invites = 0}) => {
  'chats': chats,
  'unread': 0,
  'requests': requests,
  'invites': invites,
  'more': false,
};

/// The routes You reads on opening, with the chats given.
void youRoutes(Api api, List<Map<String, dynamic>> chats, {int requests = 0}) {
  api.routes['GET /v1/me/conversations'] = (r) => r.url.queryParameters['archived'] == '1' ? chatsList([], requests: requests) : chatsList(chats, requests: requests);
  api.routes['GET /v1/me/people'] = (r) => {
    'chats': [for (final c in chats.where((c) => c['kind'] == 'person')) c],
    'unread': 0,
    'requests': requests,
    'more': false,
  };
  api.routes['GET /v1/me/chats'] = (_) => {'chats': <Object>[], 'unread': 0};
  api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
}

/// You, opened on a phone-sized screen and let load.
Future<void> openYouHome(WidgetTester t, Auth auth) async {
  t.view.physicalSize = const Size(390, 844);
  t.view.devicePixelRatio = 1;
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: auth, now: () => filmNow, presence: false)));
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 800));
}

/// The screen let go (a cut to another phone) and the toasts' timers run out.
Future<void> letGo(WidgetTester t) async {
  await t.pumpWidget(const SizedBox());
  await t.pump(const Duration(seconds: 5));
}
