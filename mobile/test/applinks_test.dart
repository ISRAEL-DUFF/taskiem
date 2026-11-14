// Links that open the app (docs/spec/mobile-app-m4.md N5): which of the
// server's addresses the app opens, and a link that waits.

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/applinks.dart';

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
}
