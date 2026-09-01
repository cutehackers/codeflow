import React, { useState } from 'react';
import { useFeed } from '../features/feed/useFeed';

export const FeedList: React.FC = () => {
  const { posts, likePost, addComment } = useFeed();
  const [commentText, setCommentText] = useState('');

  const onLikeClick = async (postId: string) => {
    await likePost(postId);
  };

  const onCommentSubmit = async (e: React.FormEvent, postId: string) => {
    e.preventDefault();
    if (commentText.trim()) {
      await addComment(postId, commentText);
      setCommentText('');
    }
  };

  const onShare = (postId: string) => {
    navigator.clipboard.writeText(`https://app.local/post/${postId}`);
  };

  return (
    <section className="feed-list">
      {posts.map((post) => (
        <article key={post.id} className="feed-item">
          <h2>{post.title}</h2>
          <p>{post.content}</p>
          <button onClick={() => onLikeClick(post.id)}>Like ({post.likes})</button>
          <button onClick={() => onShare(post.id)}>Share</button>
          <form onSubmit={(e) => onCommentSubmit(e, post.id)}>
            <input
              value={commentText}
              onChange={(e) => setCommentText(e.target.value)}
              placeholder="Write a comment..."
            />
            <button type="submit">Comment</button>
          </form>
        </article>
      ))}
    </section>
  );
};
