// A group's media and files (docs/spec/people-chatting-5b.md A, "Group
// info screen: … media"; GET /v1/me/groups/{id}/media), in the app's own
// look (docs/spec/mobile-app-look.md): on the group's info, a section with
// the latest four as tiles and "See all"; behind it, every photo three to a
// row and every video, voice note and file a row, the newest first, older
// ones a page at a time. Only what you see of the group (your own time in
// it, none deleted for you or for everyone). Each opens at its place in
// the chat.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'attachments.dart' show fileSize;
import 'door.dart' show doorFileMessage;
import 'group_chat.dart';
import 'words.dart';

/// What the item is, in words: a photo, a video, a voice note, or the
/// file's name.
String groupMediaWords(GroupMessage m) {
  final a = m.attachment!;
  if (a.kind == 'photo') return 'Photo';
  if (a.kind == 'voice') return 'Voice note';
  if (a.type.startsWith('video/')) return 'Video';
  final n = a.name ?? '';
  return n.isEmpty ? 'File' : n;
}

String _icon(ChatAttachment a) => a.kind == 'voice' ? 'mic' : 'file';

/// The section on the group's info: the latest four, and "See all".
class GroupMediaSection extends StatefulWidget {
  const GroupMediaSection({super.key, required this.data, required this.onJump});
  final GroupChatData data;
  final ValueChanged<String> onJump;

  @override
  State<GroupMediaSection> createState() => _GroupMediaSectionState();
}

class _GroupMediaSectionState extends State<GroupMediaSection> {
  GroupMediaResponse? _page;

  @override
  void initState() {
    super.initState();
    unawaited(widget.data.media().then((p) => mounted ? setState(() => _page = p) : null));
  }

  @override
  Widget build(BuildContext context) {
    final p = _page;
    if (p == null || p.messages.isEmpty) return const SizedBox.shrink();
    final latest = p.messages.take(4).toList();
    return TkSection(
      title: 'Media and files',
      onSeeAll: () => tkPush<void>(context, GroupMediaScreen(data: widget.data, first: p, onJump: widget.onJump)),
      seeAllLabel: 'See all media and files',
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: LayoutBuilder(
            builder: (context, box) {
              final side = (box.maxWidth - 3 * 8) / 4;
              return Row(
                children: [
                  for (var i = 0; i < latest.length; i++) ...[
                    if (i > 0) const SizedBox(width: 8),
                    GroupMediaTile(m: latest[i], data: widget.data, side: side, onTap: () => widget.onJump(latest[i].id)),
                  ],
                ],
              );
            },
          ),
        ),
      ],
    );
  }
}

/// One item as a square: a photo cropped to fill it, or its icon and what
/// it is on the card's grey.
class GroupMediaTile extends StatelessWidget {
  const GroupMediaTile({super.key, required this.m, required this.data, required this.side, required this.onTap});
  final GroupMessage m;
  final GroupChatData data;
  final double side;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = m.attachment!;
    final who = m.mine ? 'you' : m.sender;
    final Widget inner;
    if (a.kind == 'photo') {
      final files = data.files;
      inner = ListenableBuilder(
        listenable: files,
        builder: (context, _) {
          final f = files.of(doorFileMessage(id: m.id, mine: m.mine, a: a, at: m.createdAt));
          return f == null ? ColoredBox(color: t.panel2) : Image.memory(f.bytes, fit: BoxFit.cover, gaplessPlayback: true);
        },
      );
    } else {
      inner = ColoredBox(
        color: t.panel2,
        child: Padding(
          padding: const EdgeInsets.all(6),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              TkIcon(_icon(a), size: 22, color: t.text),
              const SizedBox(height: 4),
              Text(groupMediaWords(m), maxLines: 1, overflow: TextOverflow.ellipsis, textAlign: TextAlign.center, style: tkText(size: 11.5, weight: 600, color: t.muted)),
            ],
          ),
        ),
      );
    }
    return Semantics(
      button: true,
      label: '${groupMediaWords(m)} from $who, open in the chat',
      excludeSemantics: true,
      child: GestureDetector(
        onTap: onTap,
        behavior: HitTestBehavior.opaque,
        child: SizedBox(width: side, height: side, child: ClipRRect(borderRadius: BorderRadius.circular(10), child: inner)),
      ),
    );
  }
}

/// Every photo, video, voice note and file of the group, the newest first.
class GroupMediaScreen extends StatefulWidget {
  const GroupMediaScreen({super.key, required this.data, required this.onJump, this.first});
  final GroupChatData data;
  final ValueChanged<String> onJump;

  /// The first page, when already read.
  final GroupMediaResponse? first;

  @override
  State<GroupMediaScreen> createState() => _GroupMediaScreenState();
}

class _GroupMediaScreenState extends State<GroupMediaScreen> {
  List<GroupMessage>? _items;
  bool _more = false;
  bool _loading = false;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    final f = widget.first;
    if (f != null) {
      _items = [...f.messages];
      _more = f.more;
    } else {
      unawaited(_reload());
    }
  }

  Future<void> _reload() async {
    final p = await widget.data.media();
    if (!mounted) return;
    setState(() {
      _failed = p == null;
      if (p != null) {
        _items = [...p.messages];
        _more = p.more;
      }
    });
  }

  Future<void> _older() async {
    final items = _items;
    if (_loading || items == null || items.isEmpty) return;
    setState(() => _loading = true);
    final p = await widget.data.media(before: items.last.id);
    if (!mounted) return;
    setState(() {
      _loading = false;
      _failed = p == null;
      if (p != null) {
        _items = [...items, ...p.messages];
        _more = p.more;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final items = _items;
    final photos = [...?items?.where((m) => m.attachment!.kind == 'photo')];
    final rest = [...?items?.where((m) => m.attachment!.kind != 'photo')];
    final now = widget.data.now();
    return TkScreen(
      title: 'Media and files',
      onRefresh: _reload,
      children: [
        if (items == null && !_failed) const TkSkeleton(rows: 3, label: 'Loading the media…'),
        if (items == null && _failed) const TkSection(empty: 'The media can’t be read now. Pull to try again.'),
        if (items != null && items.isEmpty) const TkSection(empty: 'No photos, voice notes or files yet.'),
        if (photos.isNotEmpty)
          TkSection(
            title: 'Photos',
            children: [
              Padding(
                padding: const EdgeInsets.all(12),
                child: LayoutBuilder(
                  builder: (context, box) {
                    final side = (box.maxWidth - 2 * 8) / 3;
                    return Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: [for (final m in photos) GroupMediaTile(m: m, data: widget.data, side: side, onTap: () => widget.onJump(m.id))],
                    );
                  },
                ),
              ),
            ],
          ),
        if (rest.isNotEmpty)
          TkSection(
            title: 'Videos, voice notes and files',
            inset: 56,
            children: [
              for (final m in rest)
                TkListRow(
                  title: groupMediaWords(m),
                  sub: [m.mine ? 'You' : m.sender, fileSize(m.attachment!.size), listTime(m.createdAt, now)].join(' · '),
                  icon: _icon(m.attachment!),
                  square: m.attachment!.kind == 'voice' ? look.teal : look.blue,
                  semanticsLabel: '${groupMediaWords(m)} from ${m.mine ? 'you' : m.sender}, open in the chat',
                  onTap: () => widget.onJump(m.id),
                ),
            ],
          ),
        if (_more) Center(child: TkSmallButton(label: _loading ? 'Loading…' : 'Show older', onPressed: _loading ? null : _older)),
      ],
    );
  }
}
