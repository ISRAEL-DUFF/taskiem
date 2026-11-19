// The end of a list read a page at a time: the next page is read when
// this comes near the screen as the person scrolls (or at once, when the
// list is short), and a button reads it too (a screen reader's way).

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';

import 'kit.dart';

/// Put at the end of a list with more pages: reads the next with [onMore]
/// as it comes within [ahead] of the screen, one at a time.
class TkLoadMore extends StatefulWidget {
  const TkLoadMore({super.key, required this.onMore, this.label = 'Show more', this.ahead = 400});
  final Future<void> Function() onMore;
  final String label;
  final double ahead;

  @override
  State<TkLoadMore> createState() => _TkLoadMoreState();
}

class _TkLoadMoreState extends State<TkLoadMore> {
  ScrollPosition? _pos;
  bool _busy = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final pos = Scrollable.maybeOf(context)?.position;
    if (pos != _pos) {
      _pos?.removeListener(_check);
      _pos = pos?..addListener(_check);
    }
    WidgetsBinding.instance.addPostFrameCallback((_) => _check());
  }

  @override
  void dispose() {
    _pos?.removeListener(_check);
    super.dispose();
  }

  /// Near the screen: the next page.
  void _check() {
    final pos = _pos;
    if (!mounted || _busy || pos == null || !pos.hasPixels || !pos.hasViewportDimension) return;
    final box = context.findRenderObject();
    if (box is! RenderBox || !box.attached || !box.hasSize) return;
    final vp = RenderAbstractViewport.maybeOf(box);
    if (vp == null) return;
    final top = vp.getOffsetToReveal(box, 0).offset;
    if (top <= pos.pixels + pos.viewportDimension + widget.ahead) _go();
  }

  Future<void> _go() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await widget.onMore();
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 8),
    child: Center(child: TkSmallButton(label: _busy ? 'Loading…' : widget.label, onPressed: _busy ? null : _go)),
  );
}
