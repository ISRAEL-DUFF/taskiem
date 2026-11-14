// The app as a whole, opened by what comes from outside it
// (docs/spec/mobile-app-m4.md N4, N5): a link that waited behind signing in
// opens its screen; a notification's page opens its screen; the question
// asked once after signing in; a notification with the app open is a toast
// with Open; and where each page of the web opens in the app.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/notify.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/route.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/chat/invoices_page.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';

import 'lock_test.dart' show FakeUnlocker;
import 'notify_test.dart' show FakeFirebase;
import 'support.dart';

const _bayo = 'ef99249d-9322-40ca-91c2-e24fdd450c15';

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

void main() {
  test('where each page of the web opens in the app', () {
    expect(routeOf('/chat'), (tab: tabChats, screen: RouteScreen.none, id: ''));
    expect(routeOf('/chat?with=$_bayo'), (tab: tabChats, screen: RouteScreen.conversation, id: _bayo));
    expect(routeOf('/chat?with=c-42'), (tab: tabChats, screen: RouteScreen.customer, id: '42'));
    expect(routeOf('/approvals'), (tab: tabChats, screen: RouteScreen.taskiem, id: ''));
    expect(routeOf('/invoices/INV-1'), (tab: tabMoney, screen: RouteScreen.invoice, id: 'INV-1'));
    expect(routeOf('/bills/b1'), (tab: tabMoney, screen: RouteScreen.bill, id: 'b1'));
    expect(routeOf('/purchases/p1'), (tab: tabOrders, screen: RouteScreen.purchase, id: 'p1'));
    expect(routeOf('/trade-orders/t1'), (tab: tabOrders, screen: RouteScreen.tradeOrder, id: 't1'));
    expect(routeOf('/storefront/orders/ORD-9'), (tab: tabOrders, screen: RouteScreen.storefrontOrder, id: 'ORD-9'));
    expect(routeOf('/invoices'), (tab: tabMoney, screen: RouteScreen.none, id: ''));
    expect(routeOf('/storefront/orders'), (tab: tabOrders, screen: RouteScreen.none, id: ''));
    // A bad escape opens the tab, not a crash.
    expect(routeOf('/invoices/%E0%A4'), (tab: tabMoney, screen: RouteScreen.none, id: ''));
    expect(routeOf('/settings'), (tab: tabToday, screen: RouteScreen.none, id: ''));
  });

  group('the app', () {
    setUpAll(loadFonts);

    Future<void> settle(WidgetTester t) async {
      for (var i = 0; i < 8; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
        await t.pump();
      }
    }

    Future<Prefs> noLock() async {
      final p = Prefs(MemoryKeyStore());
      await p.load();
      await p.setLock(false);
      return p;
    }

    Future<void> open(WidgetTester t, {LinkInbox? links, PhoneNotifications? notify, Future<http.Response?> Function(http.Request)? also}) async {
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final auth = Fixture('chats').auth(also: also);
      final prefs = await t.runAsync(noLock);
      await t.pumpWidget(TaskiemApp(auth: auth, unlocker: FakeUnlocker(), prefs: prefs, links: links, notify: notify));
      await settle(t);
      addTearDown(() async {
        await t.pumpWidget(const SizedBox());
        await t.pump();
      });
    }

    testWidgets('a link that came before signing in opens its screen once the app is open', (t) async {
      final inbox = LinkInbox(links: const Stream.empty(), first: () async => Uri.parse('https://staging.ispend.name.ng/approvals'));
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
      expect(inbox.waiting, isTrue);
      await open(t, links: inbox);
      expect(find.byType(TaskiemScreen), findsOneWidget);
      expect(inbox.waiting, isFalse);
    });

    testWidgets('a link to a conversation opens it, and one to an invoice its page', (t) async {
      final links = StreamController<Uri>();
      final inbox = LinkInbox(links: links.stream, first: () async => null);
      await open(t, links: inbox);
      links.add(Uri.parse('https://staging.ispend.name.ng/chat?with=$_bayo'));
      await settle(t);
      expect(find.byType(ConversationScreen), findsOneWidget);
      links.add(Uri.parse('https://staging.ispend.name.ng/invoices/INV-0002'));
      await settle(t);
      expect(find.byType(ConversationScreen), findsNothing, reason: 'what was open goes first');
      expect(find.byType(InvoicePage), findsOneWidget);
      links.add(Uri.parse('https://staging.ispend.name.ng/chat?with=c-8d3a448a-c24d-485e-93a0-ddfd438fa74e'));
      await settle(t);
      expect(find.byType(CustomerScreen), findsOneWidget);
      await links.close();
    });

    testWidgets('asked once after signing in; with the app open a notification is a toast with Open', (t) async {
      final fb = FakeFirebase();
      Future<http.Response?> also(http.Request req) async {
        if (req.url.path == '/v1/app/config') {
          return _json({'min_version': '0.1.0', 'passkeys': false, 'signup': false,
            'push': {'fcm': {'api_key': 'k', 'app_id_android': '1:1:android:a', 'project_id': 'p', 'sender_id': '1'}}});
        }
        if (req.url.path == '/v1/me/push') return _json({'devices': [], 'max': 10, 'kinds': pushKinds, 'browsers': true, 'phones': true});
        return null;
      }
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final auth = Fixture('chats').auth(also: also);
      final notify = PhoneNotifications(auth: auth, keys: MemoryKeyStore(), platform: fb);
      final prefs = await t.runAsync(noLock);
      await t.pumpWidget(TaskiemApp(auth: auth, unlocker: FakeUnlocker(), prefs: prefs, notify: notify));
      await settle(t);
      expect(find.text('Get a notification when a business or customer writes?'), findsOneWidget);
      await t.tap(find.text('Not now'));
      await settle(t);
      expect(find.text('Get a notification when a business or customer writes?'), findsNothing);
      expect(await t.runAsync(notify.shouldAsk), isFalse, reason: 'asked once');
      fb.notes.add(const PushNote(title: 'Bayo Rice Mills', body: 'Rice is 45k', data: {'link': '/chat?with=$_bayo', 'tag': _bayo}));
      await settle(t);
      expect(find.text('Bayo Rice Mills: Rice is 45k'), findsOneWidget);
      await t.tap(find.text('Open'));
      await settle(t);
      expect(find.byType(ConversationScreen), findsOneWidget);
      await t.pumpWidget(const SizedBox());
      await t.pump();
    });
  });
}
