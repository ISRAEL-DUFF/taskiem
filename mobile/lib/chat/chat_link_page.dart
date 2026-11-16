// Your chat link, and how the business takes customers' chats in the app
// (docs/spec/taskiem-for-everyone.md §8.2, §8.6, §12): the link with its
// QR code to print, the words to paste (to the phone's share sheet), **Make
// a new link** (the old one says "This link no longer works" for 30 days),
// and the settings: whether the business takes chats, its shop's chat
// button, the link at the end of updates on Taskiem's number; the closed
// reply and its words, Needs you, and telling the owner.

import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/lock.dart' show stepOut;
import '../kit/forms.dart';
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;

/// Hands words to the phone's share sheet. A test stands in for it.
Future<void> Function(String words) shareWords = (words) async {
  await stepOut(() => SharePlus.instance.share(ShareParams(text: words)));
};

/// The closed reply Taskiem sends when the business has no words of its own.
const closedDefault = 'We’re closed now. We open at 9:00 and will answer then.';

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
    final l = _link;
    final s = l?.settings;
    final r = _replies;
    final can = _manage && !_busy;
    return TkScreen(
      title: 'Your chat link',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (l != null)
          TkPanel(
            padding: const EdgeInsets.all(20),
            children: [
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text('Customers who open it chat with you here, in the Taskiem app or their browser.', style: tkHint(t)),
                  const SizedBox(height: 16),
                  if (_qr != null)
                    Center(
                      child: Container(
                        padding: const EdgeInsets.all(12),
                        decoration: BoxDecoration(color: const Color(0xFFFFFFFF), borderRadius: BorderRadius.circular(12)),
                        child: Semantics(image: true, label: 'QR code of your chat link, to print', child: Image.memory(_qr!, width: 200, height: 200, excludeFromSemantics: true)),
                      ),
                    ),
                  const SizedBox(height: 14),
                  Text(l.url, textAlign: TextAlign.center, style: tkText(size: 16, weight: 600, color: t.text)),
                  const SizedBox(height: 14),
                  TkKitField(
                    label: 'Words to paste',
                    field: false,
                    child: Container(
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(color: t.panel2, borderRadius: BorderRadius.circular(12)),
                      child: Text(l.words, style: tkText(size: 14, color: t.text)),
                    ),
                  ),
                  const SizedBox(height: 14),
                  TkButton(label: 'Share', primary: true, minHeight: 44, onPressed: () => shareWords(l.words)),
                  const SizedBox(height: 8),
                  TkButton(
                    label: 'Copy the link',
                    onPressed: () async {
                      await Clipboard.setData(ClipboardData(text: l.url));
                      if (context.mounted) showTkToast(context, 'Copied');
                    },
                  ),
                  if (_manage) ...[
                    const SizedBox(height: 8),
                    TkButton(label: 'Make a new link', enabled: !_busy, onPressed: _newLink),
                  ],
                ],
              ),
            ],
          ),
        if (s != null)
          TkSection(
            title: 'Chats in the app',
            children: [
              TkPanel(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                inset: 0,
                children: [
                  TkSwitchRow(
                    label: 'Take chats in the Taskiem app',
                    hint: 'Off: no new chats start. Chats already open go on.',
                    on: s.takesChats,
                    onChanged: can ? (v) => _settings(PutChatSettingsRequest(takesChats: v)) : null,
                  ),
                  TkSwitchRow(
                    label: 'Your link at the end of updates',
                    hint: 'On updates you send from Taskiem’s WhatsApp number: “Reply in the Taskiem app”.',
                    on: s.updatesLink,
                    onChanged: can ? (v) => _settings(PutChatSettingsRequest(linkInUpdates: v)) : null,
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        Text('Your shop’s chat button', style: tkText(size: 15, weight: 600, color: t.text)),
                        const SizedBox(height: 8),
                        for (final (k, words) in const [('both', 'Chat with us and Chat on WhatsApp'), ('taskiem', 'Chat with us, in Taskiem'), ('whatsapp', 'Chat on WhatsApp')])
                          TkChoiceRow(
                            control: TkRadio(checked: s.storefrontButton == k),
                            label: Text(words, style: tkText(size: 14, color: t.text)),
                            onTap: can && s.storefrontButton != k ? () => _settings(PutChatSettingsRequest(storefrontButton: k)) : null,
                          ),
                      ],
                    ),
                  ),
                ],
              ),
            ],
          ),
        if (r != null)
          TkSection(
            title: 'When nobody answers',
            children: [
              TkPanel(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                inset: 0,
                children: [
                  TkSwitchRow(
                    label: 'Answer when you’re closed',
                    hint: 'Once a day, in your name, to a customer who writes while your shop is closed.',
                    on: r.closedReply,
                    onChanged: can ? (v) => _replyWith(PutCustomerChatSettingsRequest(closedReply: v)) : null,
                  ),
                  if (r.closedReply)
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 12),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          TkKitField(
                            label: 'Your words',
                            hint: 'Empty: “$closedDefault”',
                            child: TkTextBox(controller: _words, lines: 2, max: 300, enabled: _manage, hint: closedDefault),
                          ),
                          if (_manage) ...[
                            const SizedBox(height: 8),
                            TkButton(label: 'Save the words', enabled: !_busy, onPressed: () => _replyWith(PutCustomerChatSettingsRequest(closedWords: _words.text.trim()))),
                          ],
                        ],
                      ),
                    ),
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
            ],
          ),
        if (!_manage && l != null) Text('The owner, or someone who manages your shop, changes these.', style: tkHint(t)),
      ],
    );
  }
}

/// Settings > Customer chats: the way to Your chat link and its settings.
class CustomerChatsCard extends StatelessWidget {
  const CustomerChatsCard({super.key, required this.auth});
  final Auth auth;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.only(top: 16),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 20),
        decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.border), borderRadius: BorderRadius.circular(Tk.radius)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Semantics(header: true, child: Text('Customer chats', style: tkText(size: 16, weight: 600, lineHeight: 1.3, color: t.text))),
            const SizedBox(height: 6),
            Text('Customers chat with you in the Taskiem app from your chat link: on your shop, a flyer or a receipt.', style: tkHint(t)),
            const SizedBox(height: 12),
            TkButton(label: 'Your chat link', onPressed: () => tkPush<void>(context, ChatLinkPage(auth: auth))),
          ],
        ),
      ),
    );
  }
}
