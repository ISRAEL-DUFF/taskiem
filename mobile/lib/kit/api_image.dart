// A picture read through the API with the session's token.

import 'package:flutter/services.dart' show Uint8List;
import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../theme/theme.dart';

/// A picture through the API, signed in (a profile's logo and photos, a
/// linked business's product photos); the ground shows until it comes,
/// and stays when it cannot be read.
class ApiImage extends StatefulWidget {
  const ApiImage({super.key, required this.transport, required this.path, this.width, this.height});
  final Transport transport;
  final String path;
  final double? width;
  final double? height;

  @override
  State<ApiImage> createState() => _ApiImageState();
}

class _ApiImageState extends State<ApiImage> {
  /// The pictures read lately, the oldest let go past [_keep].
  static final _cache = <String, Uint8List>{};
  static const _keep = 80;
  Uint8List? _bytes;

  @override
  void initState() {
    super.initState();
    _bytes = _cache[widget.path];
    if (_bytes == null) _get();
  }

  @override
  void didUpdateWidget(ApiImage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.path == widget.path) return;
    // Another picture: not the last one meanwhile.
    _bytes = _cache[widget.path];
    if (_bytes == null) _get();
  }

  Future<void> _get() async {
    final path = widget.path;
    try {
      final b = Uint8List.fromList(await widget.transport.bytes(path));
      _cache.remove(path);
      _cache[path] = b;
      while (_cache.length > _keep) {
        _cache.remove(_cache.keys.first);
      }
      // Answered late, for a picture no longer asked for: not shown.
      if (mounted && widget.path == path) setState(() => _bytes = b);
    } on Object {
      // Not drawn: the ground shows.
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = _bytes;
    if (b == null) return Container(width: widget.width, height: widget.height, color: t.tkSoft);
    return Image.memory(b, width: widget.width, height: widget.height, fit: BoxFit.cover, gaplessPlayback: true, excludeFromSemantics: true);
  }
}

