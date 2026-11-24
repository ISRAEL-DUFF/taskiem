// A delivery's proof on the app's Confirm card (build-plan-gaps H3,
// g-dev-confirm; web ChatAlerts.tsx ProofOf): the photo, the masked map
// pin (opening a map, never the coordinates in words), that the customer
// signed (opening what they drew), and which was read by the phone and
// which was sent.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/alerts.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart';

/// A small photo of a parcel at a door: a 16×16 PNG, warm brown.
final _photo = base64Decode(
  'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAFklEQVR4nGOYludGEmIY1TCqYfhqAADAp0oQ9B5fiwAAAABJRU5ErkJggg==',
);

Map<String, dynamic> _confirm({required Map<String, dynamic> proof, String line = '₦1,000 fee waits on your word'}) => {
  'key': 'confirm:d1',
  'kind': 'confirm',
  'at': '2026-11-11T13:30:00Z',
  'title': 'Confirm AD-7Q4K2P, delivered by Musa Bello',
  'line': line,
  'id': 'd1',
  'reference': 'AD-7Q4K2P',
  'amount_kobo': 100000,
  'proof': proof,
};

void main() {
  setUpAll(loadFonts);

  test('the words: which was read by the phone and which was sent', () {
    final w = proofWords(ChatAlertProof.fromJson({'mark': 'said_by_rider', 'photo_source': 'camera', 'signed': true}));
    expect(w.where, 'Location sent by the rider (a pin they placed)');
    expect(w.photo, 'Photo taken in the app');
    expect(w.signed, 'Signed by the customer');
    final at = proofWords(ChatAlertProof.fromJson({'mark': 'confirmed_at_door', 'photo_source': 'sent'}));
    expect(at.where, 'Location read by the rider’s phone, at the door');
    expect(at.photo, 'Photo sent by the rider');
    expect(at.signed, isNull);
    expect(
      pinLink(ChatAlertProofWhere.fromJson({'lat': 6.4281, 'lng': 3.4219, 'source': 'device'})).toString(),
      'https://www.google.com/maps?q=6.428,3.422',
    );
  });

  testWidgets('the Confirm card shows the pin, the photo and who signed', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final was = proofImage;
    proofImage = (_) => MemoryImage(_photo);
    addTearDown(() => proofImage = was);
    final opened = <Uri>[];
    final wasOpen = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = wasOpen);

    final f = Fixture('chat-taskiem');
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/chat-alerts') {
          return http.Response(
            jsonEncode({
              'alerts': [
                _confirm(
                  line: '₦1,000 fee waits on your word · location read by the rider’s phone at the door · photo taken in the app',
                  proof: {
                    'where': {'lat': 6.428, 'lng': 3.422, 'source': 'device', 'accuracy_m': 9},
                    'mark': 'confirmed_at_door',
                    'photo_url': 'https://files.test/proof.jpg?sig=1',
                    'photo_source': 'camera',
                    'signed': true,
                    'signature_url': 'https://files.test/signature.png?sig=2',
                  },
                ),
              ],
            }),
            200,
            headers: {'content-type': 'application/json'},
          );
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(seconds: 2));
    final proof = find.byType(DeliveryProof);
    await t.scrollUntilVisible(proof, 200, scrollable: find.byType(Scrollable).last);
    // The photo decoded, as a phone has it by the time it is looked at.
    await t.runAsync(() => precacheImage(proofImage('https://files.test/proof.jpg?sig=1'), t.element(proof)));
    await t.pump(const Duration(milliseconds: 500));
    expect(find.text('Location read by the rider’s phone, at the door'), findsOneWidget);
    expect(find.text('Photo taken in the app'), findsOneWidget);
    expect(find.text('Signed by the customer'), findsOneWidget);
    expect(find.byWidgetPredicate((w) => w is Image && w.semanticLabel == 'The photo of delivery AD-7Q4K2P'), findsOneWidget);
    // Never the coordinates in words.
    expect(find.textContaining('6.428'), findsNothing);
    await looks(t, 'confirm-proof');

    // The pin opens a map at its masked point; the signature what was drawn.
    await t.tap(find.bySemanticsLabel(RegExp('^Open the place on a map')));
    await t.tap(find.bySemanticsLabel('See what the customer signed'));
    expect(opened.map((u) => '$u'), ['https://www.google.com/maps?q=6.428,3.422', 'https://files.test/signature.png?sig=2']);
  });

  testWidgets('a pin sent in a chat, a photo sent, no signature: said so', (t) async {
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: Center(
          child: DeliveryProof(
            reference: 'AD-1',
            proof: ChatAlertProof.fromJson({
              'where': {'lat': 6.4, 'lng': 3.4, 'source': 'sent', 'coarse': true},
              'mark': 'said_by_rider',
              'photo_source': 'sent',
            }),
          ),
        ),
      ),
    );
    expect(find.text('Location sent by the rider (a pin they placed) (rough)'), findsOneWidget);
    expect(find.text('Photo sent by the rider'), findsOneWidget);
    expect(find.text('Signed by the customer'), findsNothing);
    expect(find.byWidgetPredicate((w) => w is Image), findsNothing);
  });
}
