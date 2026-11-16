// A business's chat link opened in the app (docs/spec/taskiem-for-everyone.md
// §8.1, §8.2): `https://<host>/m/{code}` shows the business's card from
// `GET /v1/chat-links/{code}` (its name, photo, hours and whether it is
// open now) and starts the chat with it (`POST /v1/me/chats {code}`). Signed
// out, the card shows over the phone way in, which passes the code on
// (`chat_code`), and the chat starts once signed in. A link replaced more
// than 30 days ago, or never made, says "This link no longer works".

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../chat/chats.dart' show ChatAvatar;
import '../chat/my_chat.dart';
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../theme/theme.dart';
import 'you.dart' show ChangeNumberScreen;

/// The code of a chat link's page ("/m/abc"), or null.
String? chatCodeOf(String? page) {
  if (page == null) return null;
  final m = RegExp(r'^/m/([A-Za-z0-9]{4,40})$').firstMatch(page);
  return m?[1];
}

const _days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

/// A business's hours, a line a day: "Mon 09:00–18:00", "Sun Closed".
List<(String, String)> hoursLines(Map<String, dynamic>? hours) {
  final days = hours?['days'];
  if (days is! List || days.length != 7) return const [];
  return [
    for (var i = 0; i < 7; i++)
      (
        _days[i],
        (days[i] is List && (days[i] as List).isNotEmpty)
            ? (days[i] as List).whereType<Map<String, dynamic>>().map((s) => '${s['open']}–${s['close']}').join(', ')
            : 'Closed',
      ),
  ];
}

/// The words of a link that no longer works.
const linkGone = 'This link no longer works';

/// The business's card: photo (or its letters), name, what it says of
/// itself, where, open now, and its hours.
class ChatLinkCardView extends StatelessWidget {
  const ChatLinkCardView({super.key, required this.card});
  final ChatLinkCard card;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final c = card;
    final urls = c.photo?['urls'];
    final photo = urls is Map ? urls.values.whereType<String>().where((u) => u.startsWith('https://')).firstOrNull : null;
    final hours = hoursLines(c.hours);
    final avatar = ChatAvatar(name: c.name, size: 72, letterSize: 26);
    return Semantics(
      container: true,
      label: 'About ${c.name}',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          if (photo != null)
            ClipOval(
              child: Image.network(photo, width: 72, height: 72, fit: BoxFit.cover, excludeFromSemantics: true, errorBuilder: (_, _, _) => avatar),
            )
          else
            avatar,
          const SizedBox(height: 12),
          Semantics(header: true, child: Text(c.name, textAlign: TextAlign.center, style: tkText(size: 22, weight: 700, lineHeight: 1.25, color: t.text))),
          if ((c.tagline ?? '').isNotEmpty) Padding(padding: const EdgeInsets.only(top: 4), child: Text(c.tagline!, textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted))),
          if ((c.area ?? '').isNotEmpty) Padding(padding: const EdgeInsets.only(top: 2), child: Text(c.area!, textAlign: TextAlign.center, style: tkHint(t))),
          if (c.openNow != null)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(c.openNow! ? 'Open now' : 'Closed now', style: tkText(size: 14, weight: 600, color: c.openNow! ? t.ok : t.muted)),
            ),
          if (hours.isNotEmpty) ...[
            const SizedBox(height: 10),
            for (final (day, line) in hours)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 1),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    SizedBox(width: 44, child: Text(day, style: tkText(size: 13, color: t.muted))),
                    Text(line, style: tkText(size: 13, color: t.text)),
                  ],
                ),
              ),
          ],
        ],
      ),
    );
  }
}

/// What the card's request answered: the card, or why there is none.
typedef CardAnswer = ({ChatLinkCard? card, String? gone});

Future<CardAnswer> readChatLinkCard(TaskiemApi api, String code) async {
  try {
    return (card: await api.getChatLinkCard(code: code), gone: null);
  } on ApiError catch (e) {
    if (e.status == 410 || e.status == 404) return (card: null, gone: e.status == 410 ? e.message : 'This link doesn’t open a chat with any business. Ask the business for its link.');
    rethrow;
  }
}

/// A chat link, signed in: the card, then **Chat with {business}**, which
/// starts the chat and opens it. [autoStart]: straight into the chat (the
/// person saw the card on the way in).
class ChatLinkScreen extends StatefulWidget {
  const ChatLinkScreen({super.key, required this.auth, required this.code, this.autoStart = false, this.onStarted});
  final Auth auth;
  final String code;
  final bool autoStart;

  /// The chat started (or the one there was): opened by the caller, in
  /// this screen's place. Without it, it is pushed over this screen.
  final void Function(PersonChat chat)? onStarted;

  @override
  State<ChatLinkScreen> createState() => _ChatLinkScreenState();
}

class _ChatLinkScreenState extends State<ChatLinkScreen> {
  ChatLinkCard? _card;
  String? _gone;
  String? _error;
  bool _needsPhone = false;
  bool _busy = false;
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final a = await readChatLinkCard(widget.auth.api, widget.code);
      if (!mounted) return;
      setState(() => (_card = a.card, _gone = a.gone, _loaded = true));
      if (widget.autoStart && a.card != null && a.card!.takesChats) unawaited(_start());
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _loaded = true));
    }
  }

  Future<void> _start() async {
    if (_busy) return;
    setState(() => (_busy = true, _error = null, _needsPhone = false));
    try {
      final chat = await widget.auth.api.startMyChat(body: StartMyChatRequest(code: widget.code));
      if (!mounted) return;
      setState(() => _busy = false);
      final open = widget.onStarted;
      if (open != null) return open(chat);
      await tkPush<void>(context, MyChatScreen(data: MyChatData(widget.auth, chat.id, notice: chat.notice)));
    } on ApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        if (e.status == 410) {
          _gone = e.message;
        } else {
          _error = e.message;
          _needsPhone = e.status == 409 && e.message.contains('phone');
        }
      });
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final c = _card;
    return TkScreen(
      title: c?.name ?? 'Chat link',
      children: [
        if (!_loaded) Center(child: Padding(padding: const EdgeInsets.all(24), child: Text('Loading…', style: tkHint(t)))),
        if (_gone != null) LinkGone(words: _gone!),
        if (c != null && _gone == null)
          TkPanel(
            padding: const EdgeInsets.all(20),
            children: [
              ChatLinkCardView(card: c),
              const SizedBox(height: 18),
              if (c.takesChats)
                TkButton(label: _busy ? 'Opening the chat…' : 'Chat with ${c.name}', primary: true, minHeight: 44, enabled: !_busy, onPressed: _start)
              else
                Text('${c.name} isn’t taking chats in Taskiem right now.', textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted)),
            ],
          ),
        if (_error != null) TkErrorBox(_error!),
        if (_needsPhone) TkButton(label: 'Add your phone', onPressed: () => tkPush<void>(context, ChangeNumberScreen(auth: widget.auth))),
      ],
    );
  }
}

/// "This link no longer works", and why.
class LinkGone extends StatelessWidget {
  const LinkGone({super.key, required this.words});
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkPanel(
      padding: const EdgeInsets.all(20),
      children: [
        Semantics(header: true, child: Text(linkGone, textAlign: TextAlign.center, style: tkText(size: 18, weight: 700, color: t.text))),
        const SizedBox(height: 6),
        Text(words.startsWith(linkGone) ? words.substring(linkGone.length).replaceFirst(RegExp(r'^[.\s]+'), '') : words, textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted)),
      ],
    );
  }
}
