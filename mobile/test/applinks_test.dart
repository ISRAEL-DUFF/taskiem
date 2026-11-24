// Links that open the app (docs/spec/mobile-app-m4.md N5): which of the
// server's addresses the app opens, and a link that waits.

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/route.dart';

void main() {
  const server = 'https://taskiem.example';
  String? page(String u) => appPageOf(Uri.parse(u), server: server);

  test('the server’s pages the app has open in it; anything else stays in the browser', () {
    expect(page('https://taskiem.example/chat'), '/chat');
    expect(page('https://taskiem.example/chat?with=0192-abc'), '/chat?with=0192-abc');
    expect(page('https://taskiem.example/chat?with=c-12'), '/chat?with=c-12');
    expect(page('https://TASKIEM.example/approvals/'), '/approvals');
    expect(page('https://taskiem.example/invoices/0192-abc'), '/invoices/0192-abc');
    expect(page('https://taskiem.example/trade-orders/x1'), '/trade-orders/x1');
    expect(page('https://taskiem.example/storefront/orders/ORD-12'), '/storefront/orders/ORD-12');
    expect(page('https://taskiem.example/bills/b1'), '/bills/b1');
    expect(page('https://taskiem.example/purchases/p1'), '/purchases/p1');
    // A question that is not a conversation's is dropped, not passed on.
    expect(page('https://taskiem.example/chat?with=../x'), '/chat');
    for (final no in [
      'https://taskiem.example/settings',
      'https://taskiem.example/invoices',
      'https://taskiem.example/invoices/a/b',
      'https://evil.example/chat',
      'http://taskiem.example/chat',
      'https://taskiem.example.evil.example/chat',
    ]) {
      expect(page(no), isNull, reason: no);
    }
  });

  // Pages H5 (g-pages-notif): a page's link from a Tell me message's
  // button opens the page in the app with the filters it set; anything but
  // a few short words by filter id is dropped.
  test('a page’s link opens the page with its filters', () {
    expect(page('https://taskiem.example/pages/disks'), '/pages/disks');
    final withDay = page('https://taskiem.example/pages/disks?filters=%7B%22day%22%3A%22today%22%7D');
    expect(withDay, '/pages/disks?filters=%7B%22day%22%3A%22today%22%7D');
    final r = routeOf(withDay!);
    expect(r.screen, RouteScreen.pages);
    final pg = pageRouteOf(r.id, pageFiltersOf);
    expect(pg.name, 'disks');
    expect(pg.filters, {'day': 'today'});
    expect(page('https://taskiem.example/pages/disks?filters=%5B1%5D'), '/pages/disks');
    expect(page('https://taskiem.example/pages/disks?filters=%7B%22Bad%20id%22%3A%22x%22%7D'), '/pages/disks');
    expect(page('https://taskiem.example/pages/Disks'), isNull);
    expect(page('https://taskiem.example/pages/disks/edit'), isNull);
    expect(pageRouteOf('tickets', pageFiltersOf), (name: 'tickets', filters: null));
  });

  test('You’s pages open the app: the chats, a chat, Orders, an order, Discover, a profile', () {
    expect(page('https://taskiem.example/you'), '/you');
    expect(page('https://taskiem.example/you/chats/c-123'), '/you/chats/c-123');
    expect(page('https://taskiem.example/you/orders'), '/you/orders');
    expect(page('https://taskiem.example/you/orders/b1/MK-1042/'), '/you/orders/b1/MK-1042');
    expect(page('https://taskiem.example/you/discover'), '/you/discover');
    expect(page('https://taskiem.example/you/discover/b1'), '/you/discover/b1');
    expect(page('https://taskiem.example/you/chats/new?business=b1&from=discover&tool=shop'), '/you/chats/new?business=b1&tool=shop');
    expect(page('https://taskiem.example/you/chats/new?business=b1&ref=MK-1042&tool=x'), '/you/chats/new?business=b1&ref=MK-1042');
    expect(page('https://taskiem.example/you/chats/new?business=../x'), '/you');
    for (final no in ['https://taskiem.example/you/settings', 'https://taskiem.example/you/orders/b1', 'https://taskiem.example/you/discover/b1/x']) {
      expect(page(no), isNull, reason: 'You’s settings and other pages stay on the web: $no');
    }

    expect(youRouteOf('/you'), (page: YouPage.chats, id: '', ref: '', tool: null));
    expect(youRouteOf('/you/chats/c1'), (page: YouPage.chat, id: 'c1', ref: '', tool: null));
    expect(youRouteOf('/you/chats/new?business=b1&tool=book'), (page: YouPage.newChat, id: 'b1', ref: '', tool: 'book'));
    expect(youRouteOf('/you/orders/b1/MK-1042'), (page: YouPage.order, id: 'b1', ref: 'MK-1042', tool: null));
    expect(youRouteOf('/you/discover/b1'), (page: YouPage.business, id: 'b1', ref: '', tool: null));
    expect(youRouteOf('/chat'), isNull);

    // In a business's session: over the chats.
    expect(routeOf('/you/chats/c1'), (tab: tabChats, screen: RouteScreen.you, id: 'c1'));
    expect(routeOf('/you/orders/b1/MK-1042'), (tab: tabChats, screen: RouteScreen.youPage, id: '/you/orders/b1/MK-1042'));
    expect(routeOf('/you/chats/new?business=b1'), (tab: tabChats, screen: RouteScreen.youPage, id: '/you/chats/new?business=b1'));
    expect(routeOf('/you'), (tab: tabChats, screen: RouteScreen.none, id: ''));
  });

  test('a password reset’s link: its token, from the fragment no server sees', () async {
    String? token(String u) => resetTokenOf(Uri.parse(u), server: server);
    expect(token('https://taskiem.example/reset-password#token=AbC-_1234.xyZ_-9876'), 'AbC-_1234.xyZ_-9876');
    expect(token('https://taskiem.example/reset-password/#token=AbC-_1234.xyZ_-9876'), 'AbC-_1234.xyZ_-9876');
    expect(token('https://taskiem.example/reset-password'), isNull);
    expect(token('https://taskiem.example/reset-password#token=a b'), isNull);
    expect(token('https://evil.example/reset-password#token=AbC-_1234.xyZ_-9876'), isNull);
    expect(token('https://taskiem.example/chat#token=AbC-_1234.xyZ_-9876'), isNull);
    expect(page('https://taskiem.example/reset-password'), isNull, reason: 'not a page opened signed in');
    final links = StreamController<Uri>();
    final inbox = LinkInbox(links: links.stream, first: () async => null);
    await pumpEventQueue();
    links.add(Uri.parse('https://staging.ispend.name.ng/reset-password#token=AbC-_1234.xyZ_-9876'));
    await pumpEventQueue();
    expect(inbox.waiting, isFalse);
    expect(inbox.takeReset(), 'AbC-_1234.xyZ_-9876');
    expect(inbox.takeReset(), isNull);
    inbox.dispose();
    await links.close();
  });

  test('a link that came signed out waits until it is taken, once', () async {
    final links = StreamController<Uri>();
    final inbox = LinkInbox(links: links.stream, first: () async => Uri.parse('https://staging.ispend.name.ng/approvals'));
    await pumpEventQueue();
    expect(inbox.waiting, isTrue);
    expect(inbox.take(), '/approvals');
    expect(inbox.take(), isNull);
    var told = 0;
    inbox.addListener(() => told++);
    links.add(Uri.parse('https://staging.ispend.name.ng/chat?with=abc'));
    links.add(Uri.parse('https://elsewhere.example/chat'));
    await pumpEventQueue();
    expect(told, 1);
    expect(inbox.take(), '/chat?with=abc');
    inbox.dispose();
    await links.close();
  });

  test('the first link, given twice, opens once', () async {
    final links = StreamController<Uri>();
    final u = Uri.parse('https://staging.ispend.name.ng/approvals');
    final inbox = LinkInbox(links: links.stream, first: () async => u);
    await pumpEventQueue();
    expect(inbox.take(), '/approvals');
    links.add(u);
    await pumpEventQueue();
    expect(inbox.take(), isNull);
    inbox.dispose();
    await links.close();
  });
}
