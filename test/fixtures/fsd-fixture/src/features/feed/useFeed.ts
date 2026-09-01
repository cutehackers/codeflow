import { useState } from 'react';
import { feedApi } from '../../shared/api/client';
import { Post } from '../../entities/post/model';

export function useFeed() {
  const [posts, setPosts] = useState<Post[]>([
    { id: 'p1', title: 'Hello World', content: 'First post in FSD', likes: 10 },
    { id: 'p2', title: 'CodeFlow v2', content: 'Architecture tracing', likes: 42 },
  ]);

  const likePost = async (postId: string) => {
    const result = await feedApi.posts.like(postId);
    if (result.success) {
      setPosts((prev) =>
        prev.map((p) => (p.id === postId ? { ...p, likes: p.likes + 1 } : p))
      );
    }
  };

  const addComment = async (postId: string, comment: string) => {
    await feedApi.posts.comment(postId, comment);
  };

  const dispatchPostAction = (actionType: string, payload: any) => {
    console.log('Dispatching action:', actionType, payload);
  };

  return {
    posts,
    likePost,
    addComment,
    dispatchPostAction,
  };
}
