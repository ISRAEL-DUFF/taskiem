// Customer chats (docs/spec/taskiem-for-everyone.md §8.2, §8.6, §12, §15),
// from Settings > Customers > Customer chats and the chat's Customers tool,
// in the app's settings look (docs/spec/mobile-app-look.md, "Settings"):
// your chat link with its QR code to print, the words to paste (to the
// phone's share sheet), **Make a new link** (the old one says "This link no
// longer works" for 30 days); whether the business takes chats (never while
// its Orders area is off), its shop's chat button, the link at the end of
// updates on Taskiem's number; Chat on WhatsApp on the link, for a business
// with its own number (§11, `link_whatsapp`); the closed reply and its words, Needs you,
// and telling the owner; and **Send an offer** to every customer who said
// yes to offers.

import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/lock.dart' show stepOut;
import '../app/session.dart' show uuid4;
import '../kit/forms.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart' show TkButtonRow, TkEyebrow, TkTopCard;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../screens/settings_more.dart' show SettingsCard, SettingsPage;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;

/// Hands words to the phone's share sheet. A test stands in for it.
Future<void> Function(String words) shareWords = (words) async {
  await stepOut(() => SharePlus.instance.share(ShareParams(text: words)));
};

/// The closed reply Taskiem sends when the business has no words of its own.
const closedDefault = 'We’re closed now. We open at 9:00 and will answer then.';

/// Copies the link, and says so.
Future<void> copyLink(BuildContext context, String url) async {
  await Clipboard.setData(ClipboardData(text: url));
  if (context.mounted) showTkToast(context, 'Copied');
}

class ChatLinkPage extends StatefulWidget {
  const ChatLinkPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<ChatLinkPage> createState() => _ChatLinkPageState();
}

class _ChatLinkPageState extends State<ChatLinkPage> {
  ChatLink? _link;
  Uint8List? _qr;
  CustomerChatSettings? _replies;
  String? _error;
  bool _busy = false;
  final _words = TextEditingController();

  Auth get _auth => widget.auth;
  bool get _manage => _auth.can('storefront.manage');

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _words.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    await Future.wait([
      () async {
        try {
          final l = await _auth.api.getChatLink();
          if (!mounted) return;
          setState(() => (_link = l, _error = null));
          await _loadQr(l);
        } on Object catch (e) {
          if (mounted) setState(() => _error = sayFailure(e));
        }
      }(),
      () async {
        try {
          final r = await _auth.api.getCustomerChatSettings();
          if (!mounted) return;
          setState(() => _replies = r);
          _words.text = r.closedWords;
        } on Object {
          // Shown without them.
        }
      }(),
    ]);
  }

  Future<void> _loadQr(ChatLink l) async {
    try {
      final b = await _auth.transport.bytes('${l.qr}${l.qr.contains('?') ? '&' : '?'}format=png');
      if (mounted) setState(() => _qr = Uint8List.fromList(b));
    } on Object {
      // The link and its words still show.
    }
  }

  Future<void> _run(Future<void> Function() f, {String? done}) async {
    if (_busy) return;
    setState(() => (_busy = true, _error = null));
    try {
      await f();
      if (done != null && mounted) showTkToast(context, done);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _newLink() async {
    final yes = await showTkConfirm(
      context,
      title: 'Make a new link?',
      body: 'Your old link will say “This link no longer works” for 30 days. Your shop page gets the new one. Chats already open go on.',
      ok: 'Make a new link',
    );
    if (!yes) return;
    await _run(() async {
      final l = await _auth.api.newChatLink();
      if (!mounted) return;
      setState(() => (_link = l, _qr = null));
      await _loadQr(l);
    }, done: 'Your new link is ready');
  }

  Future<void> _settings(PutChatSettingsRequest body) => _run(() async {
    final s = await _auth.api.putChatSettings(body: body);
    final l = _link;
    if (l != null && mounted) setState(() => _link = ChatLink(code: l.code, createdAt: l.createdAt, qr: l.qr, settings: s, url: l.url, words: l.words));
  }, done: 'Saved');

  Future<void> _replyWith(PutCustomerChatSettingsRequest body) => _run(() async {
    final r = await _auth.api.putCustomerChatSettings(body: body);
    if (mounted) setState(() => _replies = r);
  }, done: 'Saved');

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final l = _link;
    final s = l?.settings;
    final r = _replies;
    final can = _manage && !_busy;
    final ordersOff = s?.ordersOff == true;
    return SettingsPage(
      title: 'Customer chats',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (l == null && _error == null) const TkLoading(label: 'Loading your chat link…'),
        if (l != null)
          // The link large on the top card, its QR code to print, and what
          // to do with it.
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Your chat link', color: look.limeInk, icon: 'b-qr'),
                const SizedBox(height: 6),
                Text('Customers who open it chat with you here, in the Taskiem app or their browser.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
                if (_qr != null) ...[
                  const SizedBox(height: 14),
                  Center(
                    child: Container(
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(color: const Color(0xFFFFFFFF), borderRadius: BorderRadius.circular(16)),
                      child: Semantics(image: true, label: 'QR code of your chat link, to print', child: Image.memory(_qr!, width: 180, height: 180, excludeFromSemantics: true)),
                    ),
                  ),
                ],
                const SizedBox(height: 14),
                Text(l.url, textAlign: TextAlign.center, style: tkDisplay(size: 17, lineHeight: 1.3, color: t.text)),
                const SizedBox(height: 12),
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(color: look.ground, borderRadius: BorderRadius.circular(14)),
                  child: Semantics(label: 'Words to paste: ${l.words}', excludeSemantics: true, child: Text(l.words, style: tkText(size: 14, lineHeight: 1.4, color: t.text))),
                ),
                const SizedBox(height: 14),
                TkButtonRow([
                  TkSmallButton(label: 'Share', icon: 'b-share', primary: true, onPressed: () => shareWords(l.words)),
                  TkSmallButton(label: 'Copy the link', onPressed: () => copyLink(context, l.url)),
                ]),
                if (_manage) ...[
                  const SizedBox(height: 4),
                  Center(child: TkLink(label: 'Make a new link', bold: true, onTap: _busy ? null : _newLink)),
                ],
              ],
            ),
          ),
        if (s != null)
          SettingsCard(
            title: 'Chats in the app',
            children: [
              if (ordersOff)
                Semantics(
                  liveRegion: true,
                  child: Text(
                    'Your Orders area is off, so customers can’t start chats with you. Turn Orders on to take chats again.',
                    style: tkText(size: 13.5, lineHeight: 1.4, color: look.orangeInk),
                  ),
                ),
              TkSwitchRow(
                label: 'Take chats in the Taskiem app',
                hint: ordersOff ? 'Off while Orders is off.' : 'Off: no new chats start. Chats already open go on.',
                on: s.takesChats,
                onChanged: can && !ordersOff ? (v) => _settings(PutChatSettingsRequest(takesChats: v)) : null,
              ),
              // Only said by a server that knows it (stage 4, §11).
              if (s.linkWhatsapp != null)
                TkSwitchRow(
                  label: 'Offer WhatsApp on your chat link',
                  hint: 'With your own WhatsApp number: your link offers Chat on WhatsApp beside Chat on Taskiem.',
                  on: s.linkWhatsapp!,
                  onChanged: can ? (v) => _settings(PutChatSettingsRequest(linkWhatsapp: v)) : null,
                ),
              TkSwitchRow(
                label: 'Your link at the end of updates',
                hint: 'On updates you send from Taskiem’s WhatsApp number: “Reply in the Taskiem app”.',
                on: s.updatesLink,
                onChanged: can ? (v) => _settings(PutChatSettingsRequest(linkInUpdates: v)) : null,
              ),
              const SizedBox(height: 8),
              Text('Your shop’s chat button', style: tkText(size: 15, weight: 600, color: t.text)),
              for (final (k, words) in const [('both', 'Chat with us and Chat on WhatsApp'), ('taskiem', 'Chat with us, in Taskiem'), ('whatsapp', 'Chat on WhatsApp')])
                TkChoiceRow(
                  control: TkRadio(checked: s.storefrontButton == k),
                  label: Text(words, style: tkText(size: 14, color: t.text)),
                  onTap: can && s.storefrontButton != k ? () => _settings(PutChatSettingsRequest(storefrontButton: k)) : null,
                ),
            ],
          ),
        if (r != null)
          SettingsCard(
            title: 'When nobody answers',
            children: [
              TkSwitchRow(
                label: 'Answer when you’re closed',
                hint: 'Once a day, in your name, to a customer who writes while your shop is closed.',
                on: r.closedReply,
                onChanged: can ? (v) => _replyWith(PutCustomerChatSettingsRequest(closedReply: v)) : null,
              ),
              if (r.closedReply) ...[
                TkKitField(
                  label: 'Your words',
                  hint: 'Empty: “$closedDefault”',
                  child: TkTextBox(controller: _words, lines: 2, max: 300, enabled: _manage, hint: closedDefault),
                ),
                if (_manage) Align(alignment: Alignment.centerLeft, child: TkSmallButton(label: 'Save the words', onPressed: _busy ? null : () => _replyWith(PutCustomerChatSettingsRequest(closedWords: _words.text.trim())))),
              ],
              TkSwitchRow(
                label: 'Needs you',
                hint: 'A customer waiting 30 minutes while you’re open shows in Needs you.',
                on: r.needsYou,
                onChanged: can ? (v) => _replyWith(PutCustomerChatSettingsRequest(needsYou: v)) : null,
              ),
              TkSwitchRow(
                label: 'Tell the owner',
                hint: 'After 2 hours with no answer, the owner is told.',
                on: r.tellOwner,
                onChanged: can ? (v) => _replyWith(PutCustomerChatSettingsRequest(tellOwner: v)) : null,
              ),
            ],
          ),
        if (!_manage && l != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text('The owner, or someone who manages your shop, changes these.', style: tkHint(t))),
        if (l != null) SettingsCard(title: 'Send an offer', children: [SendOffer(auth: _auth)]),
      ],
    );
  }
}

/// Send an offer: the words to every customer who said yes to offers (at
/// most 2 a week each; the server skips those who had theirs), and how
/// many it went to.
class SendOffer extends StatefulWidget {
  const SendOffer({super.key, required this.auth});
  final Auth auth;

  @override
  State<SendOffer> createState() => _SendOfferState();
}

class _SendOfferState extends State<SendOffer> {
  final _text = TextEditingController();
  bool _busy = false;
  String? _error;
  String? _done;

  /// One id for the same words: sent again after an answer that never
  /// came, the offer goes once.
  String? _client;
  String _clientFor = '';

  @override
  void initState() {
    super.initState();
    _text.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final words = _text.text.trim();
    if (words.isEmpty || _busy) return;
    if (words != _clientFor) {
      _client = 'offer-${uuid4()}';
      _clientFor = words;
    }
    setState(() => (_busy = true, _error = null, _done = null));
    try {
      final r = await widget.auth.api.broadcastOffer(body: BroadcastOfferRequest(text: words, clientId: _client));
      if (!mounted) return;
      _text.clear();
      _clientFor = '';
      setState(() {
        _busy = false;
        _done = r.sent == 0 && r.skipped == 0
            ? 'No customer has said yes to offers yet.'
            : [
                'Sent to ${r.sent} ${r.sent == 1 ? 'customer' : 'customers'}.',
                if (r.skipped > 0) '${r.skipped} skipped: they had 2 offers this week, or stopped messages.',
              ].join(' ');
      });
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final may = widget.auth.can('storefront.manage|order.handle');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('To every customer who said yes to offers from you, in their chat with you, marked Offer. At most 2 a week each.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
        const SizedBox(height: 10),
        TkKitField(label: 'Your offer', child: TkTextBox(controller: _text, lines: 3, max: 1000, enabled: may, hint: 'Small chops, 20% off this weekend')),
        const SizedBox(height: 10),
        Align(
          alignment: Alignment.centerLeft,
          child: TkSmallButton(label: _busy ? 'Sending…' : 'Send the offer', primary: true, onPressed: !may || _busy || _text.text.trim().isEmpty ? null : _send),
        ),
        if (_done != null) Padding(padding: const EdgeInsets.only(top: 10), child: Semantics(liveRegion: true, child: Text(_done!, style: tkText(size: 14, color: t.text)))),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
      ],
    );
  }
}

/// The chat's Customers tool: your chat link to share, and an offer to
/// those who said yes, in a sheet; the rest of Customer chats a row away.
Future<void> showCustomersSheet(BuildContext context, Auth auth) => showTkSheet<void>(context, (_) => _CustomersSheet(auth: auth));

class _CustomersSheet extends StatefulWidget {
  const _CustomersSheet({required this.auth});
  final Auth auth;

  @override
  State<_CustomersSheet> createState() => _CustomersSheetState();
}

class _CustomersSheetState extends State<_CustomersSheet> {
  ChatLink? _link;
  String? _error;

  @override
  void initState() {
    super.initState();
    widget.auth.api.getChatLink().then((l) {
      if (mounted) setState(() => _link = l);
    }, onError: (Object e) {
      if (mounted) setState(() => _error = sayFailure(e));
    });
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final l = _link;
    return TkAppLook(
      child: TkSheet(
        title: 'Customers',
        crumb: 'Customers who chat with you in the app',
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            if (_error != null) TkErrorBox(_error!),
            if (l == null && _error == null) const TkLoading(label: 'Loading your chat link…'),
            if (l != null) ...[
              TkSection(
                title: 'Your chat link',
                inset: 56,
                children: [
                  TkListRow(title: l.url, sub: 'On your shop, a flyer or a receipt', icon: 'b-qr', square: look.teal),
                  TkListRow(title: 'Share the link', icon: 'b-share', square: look.lime, onTap: () => shareWords(l.words)),
                  TkListRow(title: 'Copy the link', icon: 'b-note', square: look.blue, onTap: () => copyLink(context, l.url)),
                  TkListRow(
                    title: 'QR code and settings',
                    sub: l.settings.takesChats ? 'Taking chats' : 'Not taking chats now',
                    icon: 'b-tools',
                    square: look.violet,
                    onTap: () => tkPush<void>(context, ChatLinkPage(auth: widget.auth)),
                  ),
                ],
              ),
              const SizedBox(height: 14),
              TkFormGroup(title: 'Send an offer', children: [SendOffer(auth: widget.auth)]),
            ],
          ],
        ),
      ),
    );
  }
}
