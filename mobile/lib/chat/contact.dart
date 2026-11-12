// A conversation's contact info (web/src/pages/ContactInfo.tsx,
// Conversation.tsx MediaAndFiles; styles.css .chat-info, .info-*), as
// WhatsApp's, the whole screen on a phone: the picture and name, how we
// trade, round actions (Search, Pin), what the business says about itself,
// starred messages, media and files, Mute as a switch, Archive, and the one
// thing that cannot be undone last, in red.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'chats.dart' show ChatAvatar;
import 'conversation.dart' show ConversationData;
import 'search.dart' show sideRoute;
import 'words.dart';

/// Opens the conversation's contact info over it.
Future<void> showContactInfo(BuildContext context, {required ConversationData data, required VoidCallback onSearch, required ValueChanged<String> onJump}) {
  return Navigator.of(context).push<void>(sideRoute((context) => ContactInfoScreen(data: data, onSearch: onSearch, onJump: onJump), context));
}

class ContactInfoScreen extends StatefulWidget {
  const ContactInfoScreen({super.key, required this.data, required this.onSearch, required this.onJump});
  final ConversationData data;

  /// Search this conversation (the screen has closed).
  final VoidCallback onSearch;

  /// Go to a starred message (the screen has closed).
  final ValueChanged<String> onJump;

  @override
  State<ContactInfoScreen> createState() => _ContactInfoScreenState();
}

class _ContactInfoScreenState extends State<ContactInfoScreen> {
  bool _starredView = false;
  List<LinkMessage>? _starred;
  LinkCard? _card;
  bool _busy = false;
  bool _mediaOpen = false;

  ConversationData get d => widget.data;

  @override
  void initState() {
    super.initState();
    d.starred().then((s) => mounted ? setState(() => _starred = s) : null);
    d.card().then((c) => mounted ? setState(() => _card = c) : null);
  }

  Future<void> _set(Map<String, dynamic> body, String done) async {
    if (_busy) return;
    setState(() => _busy = true);
    final why = await d.setting(body);
    if (!mounted) return;
    setState(() => _busy = false);
    showTkToast(context, why ?? done, error: why != null);
  }

  Future<void> _end(BusinessLink link) async {
    final yes = await showTkConfirm(
      context,
      title: 'End the link with ${link.name}?',
      body: 'What passed between you stays on both sides.',
      ok: 'End link',
      danger: true,
    );
    if (!yes || !mounted) return;
    setState(() => _busy = true);
    final why = await d.chats.answer(link, 'end');
    if (!mounted) return;
    setState(() => _busy = false);
    showTkToast(context, why ?? 'Link ended', error: why != null);
  }

  void _close() => Navigator.of(context).pop();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final top = MediaQuery.paddingOf(context).top;
    return ColoredBox(
      color: t.panel,
      child: ListenableBuilder(
        listenable: d.chats,
        builder: (context, _) {
          final c = d.current;
          return Semantics(
            container: true,
            scopesRoute: true,
            namesRoute: true,
            explicitChildNodes: true,
            label: _starredView ? 'Starred messages with ${c.name}' : 'About ${c.name}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                SizedBox(height: top),
                _Head(
                  title: _starredView ? 'Starred messages' : 'Contact info',
                  back: _starredView,
                  onTap: _starredView ? () => setState(() => _starredView = false) : _close,
                ),
                Expanded(child: _starredView ? _starredList(t) : _info(t)),
              ],
            ),
          );
        },
      ),
    );
  }

  Widget _starredList(TkTokens t) {
    final list = _starred ?? const <LinkMessage>[];
    final c = d.current;
    if (list.isEmpty) {
      return Padding(
        padding: const EdgeInsets.all(16),
        child: Text('Nothing starred in this chat. Open a message’s menu and choose Star.', style: tkHint(t)),
      );
    }
    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final m in list)
            Semantics(
              button: true,
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () {
                  _close();
                  widget.onJump(m.id);
                },
                child: Container(
                  // `.info-starred { padding: 10px 16px; gap: 2px }`, a line under it.
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                  decoration: BoxDecoration(border: Border(bottom: BorderSide(color: t.border))),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(previewLine(m, c.name), style: tkText(size: 14, color: t.text)),
                      const SizedBox(height: 2),
                      Text(whenWords(m.createdAt, d.chats.now()), style: tkHint(t)),
                    ],
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  Widget _info(TkTokens t) {
    final c = d.current;
    final link = d.link;
    final manage = d.chats.auth.can('chat.trade');
    final card = _card;
    final hasCard = card != null && ((card.area ?? '').isNotEmpty || (card.tagline ?? '').isNotEmpty || card.items.isNotEmpty);
    final kept = (d.messages ?? const <LinkMessage>[]).where((m) => m.attachment != null).toList().reversed.toList();
    final line = Container(height: 1, color: t.border);
    Widget hint(String s) => Text(s, textAlign: TextAlign.center, style: tkHint(t));
    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // `.info-hero { padding: 20px 16px 16px; gap: 4px }`
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 20, 16, 16),
            child: Column(
              children: [
                ChatAvatar(name: c.name, logo: c.logo ? d.chats.logo(c.business) : null, size: 96, letterSize: 36),
                const SizedBox(height: 12),
                Text(c.name, textAlign: TextAlign.center, style: tkText(size: 18, weight: 600, color: t.text)),
                if (link != null) ...[const SizedBox(height: 4), hint(roleLine(link.role))],
                if (link?.state == 'active' && link?.answeredAt != null) ...[const SizedBox(height: 4), hint('Linked ${whenWords(link!.answeredAt!, d.chats.now())}')],
                if (c.state != 'active') ...[const SizedBox(height: 4), hint('The link has ended.')],
                const SizedBox(height: 18),
                Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    _Round(
                      icon: 'search',
                      label: 'Search',
                      onTap: () {
                        _close();
                        widget.onSearch();
                      },
                    ),
                    if (manage) ...[
                      const SizedBox(width: 22),
                      _Round(
                        icon: 'pin',
                        label: c.pinned ? 'Unpin' : 'Pin',
                        on: c.pinned,
                        onTap: _busy ? null : () => _set({'pinned': !c.pinned}, c.pinned ? 'Unpinned' : 'Pinned'),
                      ),
                    ],
                  ],
                ),
              ],
            ),
          ),
          if (hasCard) ...[
            line,
            // `.info-section { padding: 12px 16px }`
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
              child: Semantics(
                container: true,
                label: 'About the business',
                explicitChildNodes: true,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text('About', style: tkText(size: 13, weight: 500, color: t.muted)),
                    const SizedBox(height: 6),
                    if ((card.tagline ?? '').isNotEmpty) ...[Text(card.tagline!, style: tkText(size: 14, color: t.text)), const SizedBox(height: 4)],
                    if ((card.area ?? '').isNotEmpty) Text(card.area!, style: tkHint(t)),
                    if (card.items.isNotEmpty) ...[
                      const SizedBox(height: 6),
                      Wrap(
                        alignment: WrapAlignment.center,
                        spacing: 6,
                        runSpacing: 6,
                        children: [
                          for (final it in card.items.take(5))
                            Container(
                              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 2),
                              decoration: BoxDecoration(color: t.panel2, borderRadius: BorderRadius.circular(999)),
                              child: Text(it.name, style: tkText(size: 13, color: t.text)),
                            ),
                        ],
                      ),
                    ],
                  ],
                ),
              ),
            ),
          ],
          line,
          // `.info-section.flush { padding: 4px 0 }`
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _Row(
                  icon: 'star',
                  label: 'Starred messages',
                  trailing: [_Count(_starred == null ? '' : '${_starred!.length}'), TkIcon('chevron', size: 22, color: t.muted)],
                  onTap: () => setState(() => _starredView = true),
                ),
                if (kept.isNotEmpty) ...[
                  _Row(
                    icon: 'image',
                    label: 'Media and files',
                    expanded: _mediaOpen,
                    trailing: [
                      _Count('${kept.length}'),
                      AnimatedRotation(
                        turns: _mediaOpen ? 0.25 : 0,
                        duration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 180),
                        child: TkIcon('chevron', size: 22, color: t.muted),
                      ),
                    ],
                    onTap: () => setState(() => _mediaOpen = !_mediaOpen),
                  ),
                  if (_mediaOpen) _MediaAll(kept: kept, data: d) else _Thumbs(photos: kept.where((m) => m.attachment!.kind == 'photo').take(4).toList(), data: d),
                ],
              ],
            ),
          ),
          if (manage) ...[
            line,
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  _Row(
                    icon: 'mute',
                    label: 'Mute notifications',
                    checked: c.muted,
                    trailing: [_Switch(on: c.muted)],
                    onTap: _busy ? null : () => _set({'muted': !c.muted}, c.muted ? 'Unmuted' : 'Muted'),
                  ),
                  _Row(
                    icon: 'archive',
                    label: c.archived ? 'Unarchive this chat' : 'Archive this chat',
                    onTap: _busy ? null : () => _set({'archived': !c.archived}, c.archived ? 'Unarchived' : 'Archived'),
                  ),
                ],
              ),
            ),
          ],
          if (manage && link != null && link.state == 'active' && d.chats.manage) ...[
            line,
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: _Row(icon: 'unlink', label: 'End link with ${link.name}', danger: true, onTap: _busy ? null : () => _end(link)),
            ),
          ],
        ],
      ),
    );
  }
}

/// `.chat-info-head`: Close (or Back), the title; a line under it.
class _Head extends StatelessWidget {
  const _Head({required this.title, required this.back, required this.onTap});
  final String title;
  final bool back;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(border: Border(bottom: BorderSide(color: t.border))),
      child: Row(
        children: [
          Semantics(
            button: true,
            label: back ? 'Back to contact info' : 'Close contact info',
            excludeSemantics: true,
            child: TkPress(
              onTap: onTap,
              child: SizedBox(width: 36, height: 40, child: Center(child: TkIcon(back ? 'back' : 'close', size: 18, color: t.text))),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Semantics(header: true, child: Text(title, style: tkText(size: 16, weight: 600, color: t.text))),
          ),
        ],
      ),
    );
  }
}

/// `.info-round`: a round icon with its word under it.
class _Round extends StatelessWidget {
  const _Round({required this.icon, required this.label, this.on = false, this.onTap});
  final String icon;
  final String label;
  final bool on;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      toggled: icon == 'pin' ? on : null,
      label: label,
      excludeSemantics: true,
      child: GestureDetector(
        onTap: onTap,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 48,
              height: 48,
              decoration: BoxDecoration(color: on ? t.tint : t.panel2, shape: BoxShape.circle),
              alignment: Alignment.center,
              child: TkIcon(icon, size: 22, color: on ? t.tintText : t.text),
            ),
            const SizedBox(height: 6),
            Text(label, style: tkText(size: 13, lineHeight: 1.2, color: t.text)),
          ],
        ),
      ),
    );
  }
}

/// `.info-row`: an icon, the words, and what is on the right; 52 px.
class _Row extends StatelessWidget {
  const _Row({required this.icon, required this.label, this.trailing = const [], this.danger = false, this.checked, this.expanded, this.onTap});
  final String icon;
  final String label;
  final List<Widget> trailing;
  final bool danger;
  final bool? checked;
  final bool? expanded;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final ink = danger ? t.danger : t.text;
    return Semantics(
      button: checked == null,
      toggled: checked,
      expanded: expanded,
      label: label,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 52),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Row(
            children: [
              TkIcon(icon, size: 22, color: danger ? t.danger : t.muted),
              const SizedBox(width: 16),
              Expanded(child: Text(label, style: tkText(size: 15, lineHeight: 1.2, color: ink))),
              for (final w in trailing) ...[const SizedBox(width: 16), w],
            ],
          ),
        ),
      ),
    );
  }
}

/// `.info-count`: 14 px, muted, figures of one width.
class _Count extends StatelessWidget {
  const _Count(this.text);
  final String text;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Text(text, style: tkText(size: 14, lineHeight: 1.2, color: t.muted).copyWith(fontFeatures: const [FontFeature.tabularFigures()]));
  }
}

/// `.info-switch`: 38 × 22, the knob 18 px; the accent when on.
class _Switch extends StatelessWidget {
  const _Switch({required this.on});
  final bool on;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final d = still ? Duration.zero : const Duration(milliseconds: 180);
    return AnimatedContainer(
      duration: d,
      width: 38,
      height: 22,
      decoration: BoxDecoration(color: on ? t.accent : t.borderStrong, borderRadius: BorderRadius.circular(999)),
      child: Stack(
        children: [
          AnimatedPositioned(
            duration: d,
            top: 2,
            left: on ? 18 : 2,
            child: Container(
              width: 18,
              height: 18,
              decoration: BoxDecoration(
                color: t.panel,
                shape: BoxShape.circle,
                boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 1), blurRadius: 2)],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// `.info-thumbs`: the latest four photos, square, four to a row.
class _Thumbs extends StatelessWidget {
  const _Thumbs({required this.photos, required this.data});
  final List<LinkMessage> photos;
  final ConversationData data;

  @override
  Widget build(BuildContext context) {
    if (photos.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
      child: LayoutBuilder(
        builder: (context, box) {
          final side = (box.maxWidth - 18) / 4;
          return Row(
            children: [
              for (final m in photos) ...[
                if (m != photos.first) const SizedBox(width: 6),
                _Photo(m: m, data: data, width: side, height: side),
              ],
            ],
          );
        },
      ),
    );
  }
}

/// A photo of the conversation, cropped to fill its box, 6 px corners.
class _Photo extends StatelessWidget {
  const _Photo({required this.m, required this.data, required this.width, required this.height});
  final LinkMessage m;
  final ConversationData data;
  final double width;
  final double height;

  @override
  Widget build(BuildContext context) {
    final files = data.chats.files;
    return Semantics(
      image: true,
      label: m.mine ? 'Your photo' : 'Photo from ${data.conv.name}',
      child: ListenableBuilder(
        listenable: files,
        builder: (context, _) {
          final f = files.of(m);
          return SizedBox(
            width: width,
            height: height,
            child: f == null
                ? null
                : ClipRRect(
                    borderRadius: BorderRadius.circular(6),
                    child: Image.memory(f.bytes, fit: BoxFit.cover, gaplessPlayback: true),
                  ),
          );
        },
      ),
    );
  }
}

/// `.info-media-all`: every photo (three to a row, 96 px tall), voice
/// note and file (each a row of its own), the newest first.
class _MediaAll extends StatelessWidget {
  const _MediaAll({required this.kept, required this.data});
  final List<LinkMessage> kept;
  final ConversationData data;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: LayoutBuilder(
        builder: (context, box) {
          // `repeat(auto-fill, minmax(96px, 1fr))`, 8 px apart.
          final cols = ((box.maxWidth + 8) / 104).floor().clamp(1, 99);
          final w = (box.maxWidth - 8 * (cols - 1)) / cols;
          // Grid placement: a file or a voice note takes a row of its own;
          // photos fill the rows between.
          final rows = <Widget>[];
          var photos = <LinkMessage>[];
          void flush() {
            if (photos.isEmpty) return;
            for (var i = 0; i < photos.length; i += cols) {
              final run = photos.sublist(i, (i + cols).clamp(0, photos.length));
              rows.add(
                SizedBox(
                  // The picture's 96 px and the line's descent under it.
                  height: 101.69,
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      for (final m in run) ...[if (m != run.first) const SizedBox(width: 8), _Photo(m: m, data: data, width: w, height: 96)],
                    ],
                  ),
                ),
              );
            }
            photos = [];
          }

          for (final m in kept) {
            final a = m.attachment!;
            if (a.kind == 'photo') {
              photos.add(m);
              continue;
            }
            flush();
            if (a.kind == 'voice') {
              rows.add(
                SizedBox(
                  height: 59.69,
                  child: Align(
                    alignment: Alignment.topLeft,
                    child: VoicePlayer(m: m, files: data.chats.files, label: m.mine ? 'Your voice note' : 'Voice note from ${data.conv.name}', width: box.maxWidth),
                  ),
                ),
              );
            } else {
              rows.add(
                Semantics(
                  button: true,
                  label: '${attachmentLabel(a)}, ${fileSize(a.size)}',
                  excludeSemantics: true,
                  child: Container(
                    padding: const EdgeInsets.all(8),
                    decoration: BoxDecoration(color: t.panel2, borderRadius: BorderRadius.circular(8)),
                    child: Row(
                      children: [
                        TkIcon('file', size: 22, color: t.text),
                        const SizedBox(width: 8),
                        Expanded(child: Text(attachmentLabel(a), style: tkText(size: 13, weight: 600, color: t.text))),
                        const SizedBox(width: 8),
                        Text(fileSize(a.size), style: tkText(size: 13, color: t.muted)),
                      ],
                    ),
                  ),
                ),
              );
            }
          }
          flush();
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (var i = 0; i < rows.length; i++) ...[if (i > 0) const SizedBox(height: 8), rows[i]],
            ],
          );
        },
      ),
    );
  }
}
